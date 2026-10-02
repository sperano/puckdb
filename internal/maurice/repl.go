package maurice

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
}

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

// spinner displays a thinking animation while the LLM is working.
type spinner struct {
	mu      sync.Mutex
	running bool
	stop    chan struct{}
}

func (s *spinner) Start(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.stop = make(chan struct{})

	style := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	go func() {
		i := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Printf("\r\033[K") // clear spinner line
				return
			case <-ticker.C:
				frame := style.Render(spinnerFrames[i%len(spinnerFrames)])
				fmt.Printf("\r  %s %s", frame, label)
				i++
			}
		}
	}()
}

func (s *spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stop)
}

func formatPrompt(model string) string {
	return fmt.Sprintf("%s %s %s ",
		stylePromptLabel.Render("maurice"),
		stylePromptModel.Render("("+model+")"),
		stylePromptArrow.Render(">"),
	)
}

// RunREPL starts the interactive Maurice chat loop and blocks until the user
// exits or the context is cancelled.
func RunREPL(ctx context.Context, cfg REPLConfig) error {
	currentEntry, err := FindModel(cfg.Models, cfg.InitialModel)
	if err != nil {
		return fmt.Errorf("select initial model: %w", err)
	}

	buildService := func(entry ModelEntry) Service {
		pcfg := cfg.ProviderConfigs[entry.Provider]
		return NewService(
			llm.NewClientForProvider(entry.Provider, pcfg, entry.ID),
			cfg.MCPClient, cfg.DB, cfg.MaxHistory, cfg.MaxTokens, cfg.MaxToolRounds,
		)
	}

	mdRenderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	if err != nil {
		return fmt.Errorf("init markdown renderer: %w", err)
	}

	svc := buildService(currentEntry)
	defer func() {
		if err := svc.Close(); err != nil {
			log.Warn().Err(err).Msg("maurice service close")
		}
	}()
	model := currentEntry.ID

	log.Info().
		Str("provider", currentEntry.Provider.String()).
		Str("model", model).
		Msg("Maurice REPL started")

	fmt.Println()
	fmt.Println(styleHeader.Render("  Maurice — Hockey AI Chat"))
	fmt.Println(styleHint.Render("  /quit  /new  /history  /load <id>  /model"))
	fmt.Println()

	lines, scanErr := readLines(ctx, os.Stdin)
	spin := &spinner{}
	var conversationID *string

	for {
		fmt.Print(formatPrompt(model))
		var input string
		select {
		case <-ctx.Done():
			// First SIGINT/SIGTERM cancels the root signal context (see
			// cmd.SignalContext). Return cleanly so the deferred service,
			// MCP and DB cleanup in the callers runs; the stdin goroutine
			// stays blocked in Scan and exits with the process.
			fmt.Println()
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

		switch {
		case input == "/quit" || input == "/exit":
			return nil

		case input == "/new":
			conversationID = nil
			fmt.Println(styleHint.Render("  Started new conversation."))
			continue

		case input == "/history":
			convs, err := svc.ListConversations(ctx, cfg.ListLimit)
			if err != nil {
				fmt.Fprintln(os.Stderr, styleError.Render("  Error: "+err.Error()))
				continue
			}
			if len(convs) == 0 {
				fmt.Println(styleHint.Render("  No conversations yet."))
				continue
			}
			fmt.Println()
			for _, c := range convs {
				title := "(untitled)"
				if c.Title != nil {
					title = *c.Title
				}
				fmt.Printf("  %s  %s  %s\n",
					styleConvID.Render(c.ID),
					styleConvDate.Render(c.UpdatedAt.Format("2006-01-02 15:04")),
					styleConvTitle.Render(title),
				)
			}
			fmt.Println()
			continue

		case input == "/model" || strings.HasPrefix(input, "/model "):
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/model"))
			if arg == "" {
				printModelMenu(model, cfg.Models)
				continue
			}
			num, err := strconv.Atoi(arg)
			if err != nil || num < 1 || num > len(cfg.Models) {
				fmt.Println(styleError.Render("  Invalid selection: " + arg))
				printModelMenu(model, cfg.Models)
				continue
			}
			currentEntry = cfg.Models[num-1]
			model = currentEntry.ID
			if err := svc.Close(); err != nil {
				log.Warn().Err(err).Msg("maurice service close")
			}
			svc = buildService(currentEntry)
			fmt.Printf("  Switched to %s %s\n",
				styleMenuCurrent.Render(model),
				styleHint.Render("("+currentEntry.Provider.String()+")"),
			)
			if pcfg := cfg.ProviderConfigs[currentEntry.Provider]; currentEntry.Provider != llm.ProviderOllama && pcfg.APIKey == "" {
				fmt.Println(styleWarning.Render("  Warning: no API key configured for " + currentEntry.Provider.String()))
			}
			continue

		case strings.HasPrefix(input, "/load "):
			id := strings.TrimSpace(strings.TrimPrefix(input, "/load "))
			if id == "" {
				fmt.Println(styleHint.Render("  Usage: /load <conversation-id>"))
				continue
			}
			conv, _, err := svc.GetConversation(ctx, id)
			if err != nil {
				fmt.Fprintln(os.Stderr, styleError.Render("  Error: "+err.Error()))
				continue
			}
			conversationID = &conv.ID
			title := "(untitled)"
			if conv.Title != nil {
				title = *conv.Title
			}
			fmt.Println(styleHint.Render("  Resumed: " + title))
			continue
		}

		spin.Start("thinking...")
		resp, err := svc.Chat(ctx, conversationID, input)
		spin.Stop()

		if err != nil {
			fmt.Fprintln(os.Stderr, styleError.Render("  Error: "+err.Error()))
			continue
		}

		conversationID = &resp.ConversationID
		if len(resp.ToolsUsed) > 0 {
			fmt.Println(styleToolTag.Render("  tools: " + strings.Join(resp.ToolsUsed, ", ")))
		}
		rendered, renderErr := mdRenderer.Render(resp.Content)
		if renderErr != nil {
			fmt.Println(resp.Content)
		} else {
			fmt.Print(rendered)
		}
	}
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

// printModelMenu prints the model selection menu to stdout.
func printModelMenu(currentModel string, models []ModelEntry) {
	fmt.Println()
	fmt.Println(styleHint.Render("  Current: " + currentModel))
	fmt.Println()
	for i, m := range models {
		num := styleMenuNumber.Render(fmt.Sprintf("%d)", i+1))
		name := m.ID
		if m.ID == currentModel {
			name = styleMenuCurrent.Render(m.ID)
		}
		fmt.Printf("  %s %-36s %s\n", num, name, styleHint.Render(m.Description))
	}
	fmt.Println()
	fmt.Println(styleHint.Render("  /model <number>"))
	fmt.Println()
}
