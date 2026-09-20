package llm

// Provider identifies an LLM provider for routing.
type Provider int

const (
	ProviderAnthropic Provider = iota
	ProviderOpenAI
	ProviderOllama
)

const (
	AnthropicBaseURL = "https://api.anthropic.com"
	OpenAIBaseURL    = "https://api.openai.com/v1"
)

// String returns a human-readable provider name.
func (p Provider) String() string {
	switch p {
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderOllama:
		return "Ollama"
	default:
		return "Unknown"
	}
}

// ProviderConfig holds connection details for a single provider.
type ProviderConfig struct {
	BaseURL string
	APIKey  string
}

// ProviderConfigsInput holds the parameters needed to build provider configs.
type ProviderConfigsInput struct {
	OllamaBaseURL   string
	AnthropicAPIKey string
	OpenAIAPIKey    string
}

// NewProviderConfigs builds a map of provider configs from the given input.
func NewProviderConfigs(in ProviderConfigsInput) map[Provider]ProviderConfig {
	return map[Provider]ProviderConfig{
		ProviderAnthropic: {
			BaseURL: AnthropicBaseURL,
			APIKey:  in.AnthropicAPIKey,
		},
		ProviderOpenAI: {
			BaseURL: OpenAIBaseURL,
			APIKey:  in.OpenAIAPIKey,
		},
		ProviderOllama: {
			BaseURL: in.OllamaBaseURL,
			APIKey:  "",
		},
	}
}

// NewClientForProvider creates an LLM client for the given provider.
// Anthropic uses the native Anthropic Messages API; OpenAI and Ollama
// use the OpenAI-compatible chat-completions endpoint.
//
// Options forward to the underlying constructor — useful for the
// per-agent timeout the simulation feature needs (each agent's
// AgentConfig.TimeoutSeconds becomes WithTimeout(...) here).
func NewClientForProvider(provider Provider, cfg ProviderConfig, model string, opts ...Option) Client {
	if provider == ProviderAnthropic {
		return NewAnthropicClient(cfg.BaseURL, cfg.APIKey, model, opts...)
	}
	return NewOpenAIClient(cfg.BaseURL, cfg.APIKey, model, opts...)
}
