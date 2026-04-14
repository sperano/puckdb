package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/maurice"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	_ "modernc.org/sqlite"
)

const (
	mauriceListLimit = 20
	mauriceDBDir     = ".puckdb"
	mauriceDBFile    = "maurice.db"
	mauriceLogFile   = "maurice.log"
)

var mauriceModels = []maurice.ModelEntry{
	{ID: "claude-haiku-4-5-20251001", Description: "fastest, lightweight", Provider: llm.ProviderAnthropic},
	{ID: "claude-sonnet-4-6", Description: "fast, strong reasoning", Provider: llm.ProviderAnthropic},
	{ID: "claude-opus-4-6", Description: "highest capability", Provider: llm.ProviderAnthropic},
	//{"gpt-4o", "fast multimodal", llm.ProviderOpenAI},
	//{"o3-mini", "efficient reasoning", llm.ProviderOpenAI},
	{ID: "qwen3:32b", Description: "strong local model", Provider: llm.ProviderOllama},
	{ID: "qwen3:8b", Description: "fast local model", Provider: llm.ProviderOllama},
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
	config.InitLoggingFlags(flags, config.LogLevelWarn, mauriceLogPath())
	return cmd
}

func runMaurice(cmd *cobra.Command) error {
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

	providerConfigs := llm.NewProviderConfigs(llm.ProviderConfigsInput{
		OllamaBaseURL:   viper.GetString(config.FlagOllamaBaseURL),
		AnthropicAPIKey: viper.GetString(config.FlagAnthropicAPIKey),
		OpenAIAPIKey:    viper.GetString(config.FlagOpenAIAPIKey),
	})

	cfgPath := viper.GetString(config.FlagMauriceConfig)
	if cfgPath == "" {
		cfgPath = maurice.DefaultConfigPath()
	}
	mcpClient, err := maurice.BuildMCPClient(cfgPath)
	if err != nil {
		return fmt.Errorf("setup MCP: %w", err)
	}
	defer mcpClient.Close()

	return maurice.RunREPL(cmd.Context(), maurice.REPLConfig{
		DB:              db,
		Models:          mauriceModels,
		ProviderConfigs: providerConfigs,
		MCPClient:       mcpClient,
		MaxHistory:      maxHistory,
		MaxTokens:       maxTokens,
		MaxToolRounds:   viper.GetInt(config.FlagMauriceMaxToolRounds),
		ListLimit:       mauriceListLimit,
	})
}

// mauriceLogPath returns ~/.puckdb/maurice.log for the default log file.
// Falls back to empty string (stdout) if the home dir can't be resolved.
func mauriceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, mauriceDBDir, mauriceLogFile)
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
