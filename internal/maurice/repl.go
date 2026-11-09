package maurice

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/mcp"
)

// ModelEntry describes a single selectable model in the REPL menu.
type ModelEntry struct {
	ID          string
	Description string
	Provider    llm.Provider
}

// ErrUnknownModel reports a model ID that is not in the Maurice model registry.
var ErrUnknownModel = errors.New("unknown Maurice model")

// FindModel returns the entry in models whose ID matches id. An unknown ID is
// an error rather than a fallback, so a typo or a model missing from the
// registry fails loudly instead of being routed to the wrong provider.
func FindModel(models []ModelEntry, id string) (ModelEntry, error) {
	for _, e := range models {
		if e.ID == id {
			return e, nil
		}
	}
	return ModelEntry{}, fmt.Errorf("%w %q", ErrUnknownModel, id)
}

// REPLConfig holds all inputs required to run the interactive REPL.
type REPLConfig struct {
	DB              DB
	Models          []ModelEntry
	InitialModel    string // model ID selected at startup; must be one of Models
	ProviderConfigs map[llm.Provider]llm.ProviderConfig
	MCPClient       mcp.Client
	MaxHistory      int
	MaxTokens       int
	MaxToolRounds   int
	ListLimit       int

	// In is read line by line; Out receives the conversation and Err the
	// error lines. The command passes the process's stdin, stdout and
	// stderr: this package never touches them itself.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// errREPLStreams reports a REPLConfig without its In, Out or Err stream.
var errREPLStreams = errors.New("maurice REPL needs In, Out and Err")

// Styles for the REPL UI.
var (
	stylePromptLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))  // blue
	stylePromptModel = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	stylePromptArrow = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))  // blue
	styleToolTag     = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Italic(true) // yellow
	styleError       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))              // red
	styleHeader      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))  // white
	styleHint        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	styleMenuCurrent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))  // green
	styleMenuNumber  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))             // blue
	styleWarning     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))              // yellow
	styleConvID      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	styleConvDate    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))              // cyan
	styleConvTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))             // white
)

// spinner frames for the thinking indicator.
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is the delay between two spinner frames.
const spinnerInterval = 80 * time.Millisecond

// spinner displays a thinking animation on out while the LLM is working.
type spinner struct {
	out     io.Writer
	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func (s *spinner) Start(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})

	style := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	go func() {
		defer close(s.done)
		i := 0
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Fprint(s.out, "\r\033[K") // clear spinner line
				return
			case <-ticker.C:
				frame := style.Render(spinnerFrames[i%len(spinnerFrames)])
				fmt.Fprintf(s.out, "\r  %s %s", frame, label)
				i++
			}
		}
	}()
}

// Stop ends the animation and waits until its line is cleared, so the
// caller's next write never interleaves with a frame.
func (s *spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stop)
	<-s.done
}

func formatPrompt(model string) string {
	return fmt.Sprintf("%s %s %s ",
		stylePromptLabel.Render("maurice"),
		stylePromptModel.Render("("+model+")"),
		stylePromptArrow.Render(">"),
	)
}

// repl is the state of one RunREPL session.
type repl struct {
	cfg            REPLConfig
	out, errOut    io.Writer
	entry          ModelEntry
	svc            Service
	renderer       *glamour.TermRenderer
	spin           *spinner
	conversationID *string
}

// RunREPL starts the interactive Maurice chat loop and blocks until the user
// exits or the context is cancelled.
func RunREPL(ctx context.Context, cfg REPLConfig) error {
	if cfg.In == nil || cfg.Out == nil || cfg.Err == nil {
		return errREPLStreams
	}
	entry, err := FindModel(cfg.Models, cfg.InitialModel)
	if err != nil {
		return fmt.Errorf("select initial model: %w", err)
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	if err != nil {
		return fmt.Errorf("init markdown renderer: %w", err)
	}
	r := &repl{cfg: cfg, out: cfg.Out, errOut: cfg.Err, entry: entry, renderer: renderer, spin: &spinner{out: cfg.Out}}
	r.svc = r.buildService()
	defer r.closeService()

	log.Info().
		Str("provider", entry.Provider.String()).
		Str("model", entry.ID).
		Msg("Maurice REPL started")

	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, styleHeader.Render("  Maurice — Hockey AI Chat"))
	fmt.Fprintln(r.out, styleHint.Render("  /quit  /new  /history  /load <id>  /model"))
	fmt.Fprintln(r.out)
	return r.loop(ctx)
}

// loop reads prompts until /quit, the end of In or cancellation.
func (r *repl) loop(ctx context.Context) error {
	lines, scanErr := readLines(ctx, r.cfg.In)
	for {
		fmt.Fprint(r.out, formatPrompt(r.entry.ID))
		var input string
		select {
		case <-ctx.Done():
			// First SIGINT/SIGTERM cancels the root signal context (see
			// cmd.SignalContext). Return cleanly so the deferred service,
			// MCP and DB cleanup in the callers runs; the stdin goroutine
			// stays blocked in Scan and exits with the process.
			fmt.Fprintln(r.out)
			return nil
		case line, ok := <-lines:
			if !ok {
				return scanErr()
			}
			input = strings.TrimSpace(line)
		}
		if input == "" {
			continue
		}
		if input == "/quit" || input == "/exit" {
			return nil
		}
		if !r.command(ctx, input) {
			r.chat(ctx, input)
		}
	}
}

// command runs input when it is a slash command and reports whether it was.
func (r *repl) command(ctx context.Context, input string) bool {
	switch {
	case input == "/new":
		r.conversationID = nil
		fmt.Fprintln(r.out, styleHint.Render("  Started new conversation."))
	case input == "/history":
		r.history(ctx)
	case input == "/model" || strings.HasPrefix(input, "/model "):
		r.selectModel(strings.TrimSpace(strings.TrimPrefix(input, "/model")))
	case strings.HasPrefix(input, "/load "):
		r.load(ctx, strings.TrimSpace(strings.TrimPrefix(input, "/load ")))
	default:
		return false
	}
	return true
}

func (r *repl) buildService() Service {
	pcfg := r.cfg.ProviderConfigs[r.entry.Provider]
	return NewService(
		llm.NewClientForProvider(r.entry.Provider, pcfg, r.entry.ID),
		r.cfg.MCPClient, r.cfg.DB, ServiceConfig{
			Provider:      r.entry.Provider.String(),
			Model:         r.entry.ID,
			MaxHistory:    r.cfg.MaxHistory,
			MaxTokens:     r.cfg.MaxTokens,
			MaxToolRounds: r.cfg.MaxToolRounds,
		},
	)
}

func (r *repl) closeService() {
	if err := r.svc.Close(); err != nil {
		log.Warn().Err(err).Msg("maurice service close")
	}
}

func (r *repl) printError(err error) {
	fmt.Fprintln(r.errOut, styleError.Render("  Error: "+err.Error()))
}

func (r *repl) history(ctx context.Context) {
	convs, err := r.svc.ListConversations(ctx, LocalUserID, r.cfg.ListLimit)
	if err != nil {
		r.printError(err)
		return
	}
	if len(convs) == 0 {
		fmt.Fprintln(r.out, styleHint.Render("  No conversations yet."))
		return
	}
	fmt.Fprintln(r.out)
	for _, c := range convs {
		title := "(untitled)"
		if c.Title != nil {
			title = *c.Title
		}
		fmt.Fprintf(r.out, "  %s  %s  %s\n",
			styleConvID.Render(c.ID),
			styleConvDate.Render(c.UpdatedAt.Format("2006-01-02 15:04")),
			styleConvTitle.Render(title),
		)
	}
	fmt.Fprintln(r.out)
}

// selectModel shows the menu for an empty arg, else switches to the
// numbered model.
func (r *repl) selectModel(arg string) {
	if arg == "" {
		printModelMenu(r.out, r.entry.ID, r.cfg.Models)
		return
	}
	num, err := strconv.Atoi(arg)
	if err != nil || num < 1 || num > len(r.cfg.Models) {
		fmt.Fprintln(r.out, styleError.Render("  Invalid selection: "+arg))
		printModelMenu(r.out, r.entry.ID, r.cfg.Models)
		return
	}
	r.closeService()
	r.entry = r.cfg.Models[num-1]
	r.svc = r.buildService()
	fmt.Fprintf(r.out, "  Switched to %s %s\n",
		styleMenuCurrent.Render(r.entry.ID),
		styleHint.Render("("+r.entry.Provider.String()+")"),
	)
	if pcfg := r.cfg.ProviderConfigs[r.entry.Provider]; r.entry.Provider != llm.ProviderOllama && pcfg.APIKey == "" {
		fmt.Fprintln(r.out, styleWarning.Render("  Warning: no API key configured for "+r.entry.Provider.String()))
	}
}

func (r *repl) load(ctx context.Context, id string) {
	if id == "" {
		fmt.Fprintln(r.out, styleHint.Render("  Usage: /load <conversation-id>"))
		return
	}
	conv, _, err := r.svc.GetConversation(ctx, LocalUserID, id)
	if err != nil {
		r.printError(err)
		return
	}
	r.conversationID = &conv.ID
	title := "(untitled)"
	if conv.Title != nil {
		title = *conv.Title
	}
	fmt.Fprintln(r.out, styleHint.Render("  Resumed: "+title))
}

func (r *repl) chat(ctx context.Context, input string) {
	r.spin.Start("thinking...")
	resp, err := r.svc.Chat(ctx, ChatRequest{UserID: LocalUserID, ConversationID: r.conversationID, Message: input})
	r.spin.Stop()
	if err != nil {
		r.printError(err)
		return
	}
	r.conversationID = &resp.ConversationID
	for _, warning := range resp.Warnings {
		fmt.Fprintln(r.out, styleWarning.Render("  Warning: "+warning))
	}
	if len(resp.ToolsUsed) > 0 {
		fmt.Fprintln(r.out, styleToolTag.Render("  tools: "+strings.Join(resp.ToolsUsed, ", ")))
	}
	rendered, renderErr := r.renderer.Render(resp.Content)
	if renderErr != nil {
		fmt.Fprintln(r.out, resp.Content)
		return
	}
	fmt.Fprint(r.out, rendered)
}

// readLines scans r line by line from a dedicated goroutine into the
// returned channel, closing it on EOF or read error (retrievable via the
// returned error func once the channel is closed). Cancellation of ctx
// stops delivery, but a goroutine blocked inside Scan itself cannot be
// interrupted — it only exits on the next line, EOF, or process exit, so
// callers must select on ctx.Done rather than wait for the channel to
// close.
func readLines(ctx context.Context, r io.Reader) (<-chan string, func() error) {
	lines := make(chan string)
	scanner := bufio.NewScanner(r)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines, scanner.Err
}

// printModelMenu prints the model selection menu to out.
func printModelMenu(out io.Writer, currentModel string, models []ModelEntry) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, styleHint.Render("  Current: "+currentModel))
	fmt.Fprintln(out)
	for i, m := range models {
		num := styleMenuNumber.Render(fmt.Sprintf("%d)", i+1))
		name := m.ID
		if m.ID == currentModel {
			name = styleMenuCurrent.Render(m.ID)
		}
		fmt.Fprintf(out, "  %s %-36s %s\n", num, name, styleHint.Render(m.Description))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, styleHint.Render("  /model <number>"))
	fmt.Fprintln(out)
}
