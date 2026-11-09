package maurice

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	// In, Out and ErrOut are the REPL's input, output (prompt, menus,
	// answers) and error streams; nil means os.Stdin, os.Stdout and
	// os.Stderr.
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// markdownWrapWidth is the column at which rendered answers wrap.
const markdownWrapWidth = 100

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

func formatPrompt(model string) string {
	return fmt.Sprintf("%s %s %s ",
		stylePromptLabel.Render("maurice"),
		stylePromptModel.Render("("+model+")"),
		stylePromptArrow.Render(">"),
	)
}

// RunREPL starts the interactive Maurice chat loop and blocks until the user
// exits, the input ends or the context is cancelled.
func RunREPL(ctx context.Context, cfg REPLConfig) error {
	return runREPL(ctx, cfg, cfg.modelService)
}

// runREPL is RunREPL with the service factory injected, so tests can drive
// the loop without an LLM provider.
func runREPL(ctx context.Context, cfg REPLConfig, newService func(ModelEntry) Service) error {
	entry, err := FindModel(cfg.Models, cfg.InitialModel)
	if err != nil {
		return fmt.Errorf("select initial model: %w", err)
	}
	mdRenderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(markdownWrapWidth))
	if err != nil {
		return fmt.Errorf("init markdown renderer: %w", err)
	}
	in, out, errOut := cfg.streams()
	r := &repl{
		models:          cfg.Models,
		providerConfigs: cfg.ProviderConfigs,
		listLimit:       cfg.ListLimit,
		out:             out,
		errOut:          errOut,
		markdown:        mdRenderer,
		spin:            newSpinner(out),
		newService:      newService,
		entry:           entry,
		svc:             newService(entry),
	}
	defer r.closeService()

	log.Info().
		Str("provider", entry.Provider.String()).
		Str("model", entry.ID).
		Msg("Maurice REPL started")
	r.printBanner()

	// Cancelled on return so the stdin goroutine stops delivering lines
	// nobody reads any more.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, scanErr := readLines(ctx, in)
	for {
		fmt.Fprint(out, formatPrompt(r.entry.ID))
		select {
		case <-ctx.Done():
			// First SIGINT/SIGTERM cancels the root signal context (see
			// cmd.SignalContext). Return cleanly so the deferred service,
			// MCP and DB cleanup in the callers runs; the stdin goroutine
			// stays blocked in Scan and exits with the process.
			fmt.Fprintln(out)
			return nil
		case line, ok := <-lines:
			if !ok {
				return scanErr()
			}
			if r.handle(ctx, strings.TrimSpace(line)) {
				return nil
			}
		}
	}
}

// modelService builds the chat service for entry with the configured limits;
// NewService replaces zero limits with the defaults.
func (cfg REPLConfig) modelService(entry ModelEntry) Service {
	return NewService(
		llm.NewClientForProvider(entry.Provider, cfg.ProviderConfigs[entry.Provider], entry.ID),
		cfg.MCPClient, cfg.DB, ServiceConfig{
			Provider:      entry.Provider.String(),
			Model:         entry.ID,
			MaxHistory:    cfg.MaxHistory,
			MaxTokens:     cfg.MaxTokens,
			MaxToolRounds: cfg.MaxToolRounds,
		},
	)
}

// streams returns the REPL's input, output and error streams, defaulting to
// the process's standard streams.
func (cfg REPLConfig) streams() (io.Reader, io.Writer, io.Writer) {
	var (
		in     io.Reader = os.Stdin
		out    io.Writer = os.Stdout
		errOut io.Writer = os.Stderr
	)
	if cfg.In != nil {
		in = cfg.In
	}
	if cfg.Out != nil {
		out = cfg.Out
	}
	if cfg.ErrOut != nil {
		errOut = cfg.ErrOut
	}
	return in, out, errOut
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
