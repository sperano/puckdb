package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewClientForProvider_Anthropic(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		apiKey  string
		want    string
	}{
		{"sk-ant prefix", "https://api.anthropic.com", "sk-ant-api03-xxx", "*llm.anthropicClient"},
		{"anthropic in URL", "https://api.anthropic.com", "some-key", "*llm.anthropicClient"},
		{"ollama default", "http://localhost:11434/v1", "", "*llm.openaiClient"},
		{"openai key", "https://api.openai.com/v1", "sk-proj-xxx", "*llm.openaiClient"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClientForProvider(tt.baseURL, tt.apiKey, "test-model")
			got := assert.ObjectsAreEqual(tt.want, client)
			// Type assertion check
			switch tt.want {
			case "*llm.anthropicClient":
				_, ok := client.(*anthropicClient)
				assert.True(t, ok, "expected anthropicClient, got %T", client)
			case "*llm.openaiClient":
				_, ok := client.(*openaiClient)
				assert.True(t, ok, "expected openaiClient, got %T", client)
			default:
				t.Fatalf("unexpected type: %v (equal: %v)", tt.want, got)
			}
		})
	}
}
