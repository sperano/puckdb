package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewClientForProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		wantType string
	}{
		{"Anthropic", ProviderAnthropic, "*llm.anthropicClient"},
		{"OpenAI", ProviderOpenAI, "*llm.openaiClient"},
		{"Ollama", ProviderOllama, "*llm.openaiClient"},
	}

	cfg := ProviderConfig{BaseURL: "http://localhost", APIKey: "test-key"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClientForProvider(tt.provider, cfg, "test-model")
			switch tt.wantType {
			case "*llm.anthropicClient":
				_, ok := client.(*anthropicClient)
				assert.True(t, ok, "expected anthropicClient, got %T", client)
			case "*llm.openaiClient":
				_, ok := client.(*openaiClient)
				assert.True(t, ok, "expected openaiClient, got %T", client)
			}
		})
	}
}

func TestProviderString(t *testing.T) {
	assert.Equal(t, "Anthropic", ProviderAnthropic.String())
	assert.Equal(t, "OpenAI", ProviderOpenAI.String())
	assert.Equal(t, "Ollama", ProviderOllama.String())
}

func TestNewProviderConfigs(t *testing.T) {
	configs := NewProviderConfigs(ProviderConfigsInput{
		OllamaBaseURL:   "http://ollama:11434/v1",
		AnthropicAPIKey: "sk-ant-test",
		OpenAIAPIKey:    "sk-openai-test",
	})

	assert.Equal(t, AnthropicBaseURL, configs[ProviderAnthropic].BaseURL)
	assert.Equal(t, "sk-ant-test", configs[ProviderAnthropic].APIKey)
	assert.Equal(t, OpenAIBaseURL, configs[ProviderOpenAI].BaseURL)
	assert.Equal(t, "sk-openai-test", configs[ProviderOpenAI].APIKey)
	assert.Equal(t, "http://ollama:11434/v1", configs[ProviderOllama].BaseURL)
	assert.Empty(t, configs[ProviderOllama].APIKey)
}

func TestParseProvider(t *testing.T) {
	cases := map[string]Provider{
		"anthropic": ProviderAnthropic, " Anthropic ": ProviderAnthropic,
		"openai": ProviderOpenAI, "OLLAMA": ProviderOllama,
	}
	for name, want := range cases {
		got, err := ParseProvider(name)
		assert.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}
	_, err := ParseProvider("gemini")
	assert.Error(t, err, "no Gemini provider config exists")
}
