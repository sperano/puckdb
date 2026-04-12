package cmd

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	mauricePrompt    = "maurice> "
	mauriceListLimit = 20
	mauriceDBDir     = ".puckdb"
	mauriceDBFile    = "maurice.db"
)

type modelSuggestion struct {
	id          string
	description string
}

var mauriceModelSuggestions = []modelSuggestion{
	// Anthropic
	{"claude-sonnet-4-20250514", "Anthropic — fast, strong reasoning"},
	{"claude-opus-4-20250514", "Anthropic — highest capability"},
	// OpenAI
	{"gpt-4o", "OpenAI — fast multimodal"},
	{"o3-mini", "OpenAI — efficient reasoning"},
	// Ollama (local)
	{"qwen3:32b", "Ollama — strong local model"},
	{"qwen3:8b", "Ollama — fast local model"},
	{"llama4:scout", "Ollama — Meta Scout"},
	{"deepseek-r1:32b", "Ollama — reasoning"},
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

	baseURL := viper.GetString(config.FlagMauriceBaseURL)
	mcpURL := viper.GetString(config.FlagMauriceMCPURL)

	mcpClient := mcppkg.NewClient(mcpURL)
	defer mcpClient.Close()

	apiKey := viper.GetString(config.FlagMauriceAPIKey)
	maxHistory := viper.GetInt(config.FlagMauriceMaxHistory)
	maxTokens := viper.GetInt(config.FlagMauriceMaxTokens)
	model := viper.GetString(config.FlagMauriceModel)

	buildService := func(m string) maurice.Service {
		return maurice.NewService(
			llm.NewClientForProvider(baseURL, apiKey, m),
			mcpClient, db, maxHistory, maxTokens,
		)
	}

	svc := buildService(model)

	log.Info().
		Str("base_url", baseURL).
		Str("model", model).
		Str("mcp_url", mcpURL).
		Str("db", dbPath).
		Msg("Maurice initialized")

	fmt.Println("Maurice — Hockey AI Chat")
	fmt.Println("Type /quit to exit, /new for new conversation, /history to list, /load <id> to resume, /model [name] to switch")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var conversationID *string

	for {
		fmt.Print(mauricePrompt)
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
			newModel := strings.TrimSpace(strings.TrimPrefix(input, "/model"))
			if newModel == "" {
				fmt.Printf("Current model: %s\n\n", model)
				fmt.Println("Suggestions:")
				for _, s := range mauriceModelSuggestions {
					marker := "  "
					if s.id == model {
						marker = "* "
					}
					fmt.Printf("  %s%-36s  %s\n", marker, s.id, s.description)
				}
				fmt.Println("\nUsage: /model <model-id>")
				continue
			}
			model = newModel
			svc = buildService(model)
			fmt.Printf("Switched to model: %s\n", model)
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
		fmt.Println(resp.Content)
		fmt.Println()
	}

	return scanner.Err()
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
