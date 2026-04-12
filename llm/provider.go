package llm

import "strings"

const anthropicAPIKeyPrefix = "sk-ant-"

// NewClientForProvider creates an LLM client, auto-detecting the provider from
// the API key and base URL:
//   - API key starts with "sk-ant-" or base URL contains "anthropic" → Anthropic
//   - Otherwise → OpenAI-compatible (Ollama, OpenAI, etc.)
func NewClientForProvider(baseURL, apiKey, model string) Client {
	if isAnthropic(baseURL, apiKey) {
		return NewAnthropicClient(baseURL, apiKey, model)
	}
	return NewOpenAIClient(baseURL, apiKey, model)
}

func isAnthropic(baseURL, apiKey string) bool {
	return strings.HasPrefix(apiKey, anthropicAPIKeyPrefix) ||
		strings.Contains(baseURL, "anthropic")
}
