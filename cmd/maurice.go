package cmd

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/maurice"
	mcppkg "github.com/sperano/puckdb/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	_ "modernc.org/sqlite"
)

const (
	mauricePromptFmt = "maurice (%s) > "
	mauriceListLimit = 20
	mauriceDBDir     = ".puckdb"
	mauriceDBFile    = "maurice.db"
)

type modelEntry struct {
	id          string
	description string
	provider    llm.Provider
}

var mauriceModels = []modelEntry{
	{"claude-sonnet-4-6", "fast, strong reasoning", llm.ProviderAnthropic},
	{"claude-opus-4-6", "highest capability", llm.ProviderAnthropic},
	//{"gpt-4o", "fast multimodal", llm.ProviderOpenAI},
	//{"o3-mini", "efficient reasoning", llm.ProviderOpenAI},
	{"qwen3:32b", "strong local model", llm.ProviderOllama},
	{"qwen3:8b", "fast local model", llm.ProviderOllama},
	//{"llama4:scout", "Meta Scout", llm.ProviderOllama},
}

func cmdMaurice() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maurice",
		Short: "Interactive AI hockey chat",
		Long:  `Start an interactive REPL to chat with Maurice, the hockey AI assistant`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(),
				&config.MauriceFlags,
			)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMaurice(cmd)
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags,
		&config.MauriceFlags,
	)
	config.InitLoggingFlags(flags, config.LogLevelWarn, config.DefaultLogFile)
	return cmd
}

func runMaurice(cmd *cobra.Command) error {
	// Open SQLite for conversation persistence
	dbPath, err := mauriceDBPath()
	if err != nil {
		return err
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open SQLite: %w", err)
	}
	defer sqlDB.Close()

	db, err := maurice.NewSQLiteDB(sqlDB)
	if err != nil {
		return fmt.Errorf("init conversation store: %w", err)
	}

	maxHistory := viper.GetInt(config.FlagMauriceMaxHistory)
	maxTokens := viper.GetInt(config.FlagMauriceMaxTokens)
	startupModel := viper.GetString(config.FlagMauriceModel)

	providerConfigs := llm.NewProviderConfigs(llm.ProviderConfigsInput{
		OllamaBaseURL:   viper.GetString(config.FlagOllamaBaseURL),
		AnthropicAPIKey: viper.GetString(config.FlagAnthropicAPIKey),
		OpenAIAPIKey:    viper.GetString(config.FlagOpenAIAPIKey),
	})

	mcpClient, err := buildMCPClient()
	if err != nil {
		return fmt.Errorf("setup MCP: %w", err)
	}
	defer mcpClient.Close()

	// Resolve startup model to a registry entry, falling back to first entry.
	currentEntry := mauriceModels[0]
	for _, e := range mauriceModels {
		if e.id == startupModel {
			currentEntry = e
			break
		}
	}

	buildService := func(entry modelEntry) maurice.Service {
		cfg := providerConfigs[entry.provider]
		return maurice.NewService(
			llm.NewClientForProvider(entry.provider, cfg, entry.id),
			mcpClient, db, maxHistory, maxTokens,
		)
	}

	mdRenderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	if err != nil {
		return fmt.Errorf("init markdown renderer: %w", err)
	}

	svc := buildService(currentEntry)
	model := currentEntry.id

	log.Info().
		Str("provider", currentEntry.provider.String()).
		Str("model", model).
		Str("db", dbPath).
		Msg("Maurice initialized")

	fmt.Println("Maurice — Hockey AI Chat")
	fmt.Println("Type /quit to exit, /new for new conversation, /history to list, /load <id> to resume, /model to switch")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var conversationID *string

	for {
		fmt.Printf(mauricePromptFmt, model)
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "/quit" || input == "/exit":
			return nil

		case input == "/new":
			conversationID = nil
			fmt.Println("Started new conversation.")
			continue

		case input == "/history":
			convs, err := svc.ListConversations(cmd.Context(), mauriceListLimit)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			if len(convs) == 0 {
				fmt.Println("No conversations yet.")
				continue
			}
			for _, c := range convs {
				title := "(untitled)"
				if c.Title != nil {
					title = *c.Title
				}
				fmt.Printf("  %s  %s  %s\n", c.ID, c.UpdatedAt.Format("2006-01-02 15:04"), title)
			}
			continue

		case input == "/model" || strings.HasPrefix(input, "/model "):
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/model"))
			if arg == "" {
				printModelMenu(model)
				continue
			}
			num, err := strconv.Atoi(arg)
			if err != nil || num < 1 || num > len(mauriceModels) {
				fmt.Printf("Invalid selection: %s\n", arg)
				printModelMenu(model)
				continue
			}
			currentEntry = mauriceModels[num-1]
			model = currentEntry.id
			svc = buildService(currentEntry)
			fmt.Printf("Switched to model: %s (%s)\n", model, currentEntry.provider)
			if cfg := providerConfigs[currentEntry.provider]; currentEntry.provider != llm.ProviderOllama && cfg.APIKey == "" {
				fmt.Printf("  Warning: no API key configured for %s — API calls will fail\n", currentEntry.provider)
			}
			continue

		case strings.HasPrefix(input, "/load "):
			id := strings.TrimSpace(strings.TrimPrefix(input, "/load "))
			if id == "" {
				fmt.Println("Usage: /load <conversation-id>")
				continue
			}
			conv, _, err := svc.GetConversation(cmd.Context(), id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			conversationID = &conv.ID
			title := "(untitled)"
			if conv.Title != nil {
				title = *conv.Title
			}
			fmt.Printf("Resumed: %s\n", title)
			continue
		}

		resp, err := svc.Chat(cmd.Context(), conversationID, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		conversationID = &resp.ConversationID
		if len(resp.ToolsUsed) > 0 {
			fmt.Printf("[used: %s]\n", strings.Join(resp.ToolsUsed, ", "))
		}
		rendered, renderErr := mdRenderer.Render(resp.Content)
		if renderErr != nil {
			fmt.Println(resp.Content)
		} else {
			fmt.Print(rendered)
		}
	}

	return scanner.Err()
}

// buildMCPClient creates a MultiClient from the maurice config file,
// or falls back to an empty client if no config exists.
func buildMCPClient() (mcppkg.Client, error) {
	cfgPath := viper.GetString(config.FlagMauriceConfig)
	if cfgPath == "" {
		cfgPath = maurice.DefaultConfigPath()
	}

	cfg, err := maurice.LoadConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	if len(cfg.MCPServers) == 0 {
		log.Warn().Str("config", cfgPath).Msg("no MCP servers configured, Maurice will have no tools")
		return mcppkg.NewNoopClient(), nil
	}

	opts := make([]mcppkg.MultiClientOption, len(cfg.MCPServers))
	for i, s := range cfg.MCPServers {
		log.Info().Str("name", s.Name).Str("url", s.URL).Strs("tools", s.Tools).Msg("connecting MCP server")
		opts[i] = mcppkg.MultiClientOption{
			Client: mcppkg.NewClient(s.URL),
			Tools:  s.Tools,
		}
	}

	return mcppkg.NewMultiClient(opts...), nil
}

func printModelMenu(currentModel string) {
	fmt.Printf("Current model: %s\n\n", currentModel)
	for i, m := range mauriceModels {
		marker := "  "
		if m.id == currentModel {
			marker = "* "
		}
		fmt.Printf("  %s%d) %-36s %s — %s\n", marker, i+1, m.id, m.provider, m.description)
	}
	fmt.Println("\nUsage: /model <number>")
}

func mauriceDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, mauriceDBDir)
	if err := os.MkdirAll(dir, config.DirPermOwnerRWX); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return filepath.Join(dir, mauriceDBFile), nil
}
