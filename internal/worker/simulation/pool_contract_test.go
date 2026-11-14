package simulation

import (
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCategoriesAcceptsTheSupportedSetInAnyOrder(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateCategories(SupportedCategories()))
	reversed := SupportedCategories()
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	require.NoError(t, ValidateCategories(reversed))
}

func TestValidateCategoriesNamesEveryProblem(t *testing.T) {
	t.Parallel()
	// The shape every shipped example used before it was fixed: seven
	// categories, no +/- and no GAA.
	err := ValidateCategories([]string{"G", "A", "PIM", "PPP", "SOG", "W", "GA", "G", "SV%"})
	require.ErrorIs(t, err, ErrUnsupportedPoolShape)
	assert.EqualError(t, err, `unsupported pool shape: duplicate category "G", unknown category "SV%", `+
		`missing category "+/-", missing category "GAA"; the simulator scores exactly [G, A, +/-, PIM, PPP, SOG, W, GA, GAA]`)
}

func TestValidateCategoriesRejectsEmpty(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, ValidateCategories(nil), ErrUnsupportedPoolShape)
}

func TestValidateRosterPositionsAcceptsTheSupportedRoster(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateRosterPositions(SupportedRosterPositions()))
}

func TestValidateRosterPositionsNamesEveryProblem(t *testing.T) {
	t.Parallel()
	got := map[RosterSlot]int{SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2, SlotBN: 5, "F": 1}
	err := ValidateRosterPositions(got)
	require.ErrorIs(t, err, ErrUnsupportedPoolShape)
	assert.EqualError(t, err, `unsupported pool shape: unknown slot "F", Util count 0, want 1, BN count 5, want 6, `+
		`IR count 0, want 3; the simulator supports exactly 2 C, 2 LW, 2 RW, 3 D, 1 Util, 2 G, 6 BN, 3 IR`)
}

func TestSupportedShapeIsACopy(t *testing.T) {
	t.Parallel()
	cats := SupportedCategories()
	cats[0] = "changed"
	roster := SupportedRosterPositions()
	roster[SlotC] = 99
	assert.Equal(t, "G", v1FixedCategories[0])
	assert.Equal(t, 2, v1FixedRoster[SlotC])
}

func TestRosterCapacityCountsEverySlot(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 21, RosterCapacity(SupportedRosterPositions()))
	assert.Equal(t, 0, RosterCapacity(nil))
}

func TestValidateProviderMatchesNewAgent(t *testing.T) {
	t.Parallel()
	configs := llm.NewProviderConfigs(llm.ProviderConfigsInput{OllamaBaseURL: "http://localhost:11434/v1"})
	for _, provider := range []string{"anthropic", "OpenAI", "ollama", "google", "gemini", "", "claude", "mistral"} {
		_, agentErr := NewAgent(1, AgentConfig{Provider: provider, Model: "m"}, configs, 2)
		validateErr := ValidateProvider(provider)
		assert.Equal(t, agentErr != nil, validateErr != nil,
			"provider %q: NewAgent err %v, ValidateProvider err %v", provider, agentErr, validateErr)
	}
	for _, ok := range []string{"anthropic", "OpenAI", "ollama", "google", "gemini"} {
		assert.NoError(t, ValidateProvider(ok), ok)
	}
}

func TestValidateAPIBase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, apiBase string }{
		{"anthropic", ""},
		{"ollama", ""},
		{"anthropic", "https://api.anthropic.com"},
		{"OpenAI", "https://API.openai.com/v1"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai/"},
		{"ollama", "http://ollama.local:11434/v1"},
		{"ollama", "https://10.0.0.5/v1"},
	} {
		assert.NoError(t, ValidateAPIBase(tc.provider, tc.apiBase), "%s %s", tc.provider, tc.apiBase)
	}
	// secret is the credential part of apiBase, which the error must not
	// echo even partially.
	for _, tc := range []struct{ provider, apiBase, secret string }{
		{"anthropic", "https://collector.example", ""},
		{"anthropic", "http://api.anthropic.com", ""},
		{"anthropic", "https://api.anthropic.com:8443", ""},
		{"anthropic", "https://api.anthropic.com.collector.example", ""},
		{"openai", "https://api.anthropic.com/v1", ""},
		{"google", "https://api.openai.com/v1", ""},
		{"google", "", ""},
		{"gemini", "", ""},
		{"gemini", "https://collector.example", ""},
		{"OpenAI", "https://collector.example/v1", ""},
		{"openai", "https://collector.example/v1?key=sk-secret", "sk-secret"},
	} {
		err := ValidateAPIBase(tc.provider, tc.apiBase)
		require.ErrorIs(t, err, ErrDisallowedAPIBase, "%s %s", tc.provider, tc.apiBase)
		assertNoAPIBaseEcho(t, err, tc.apiBase, tc.secret)
	}
	for _, tc := range []struct{ provider, apiBase, secret string }{
		{"ollama", "ollama.local:11434", ""},
		{"ollama", "/v1", ""},
		{"ollama", "ftp://ollama.local/v1", ""},
		{"ollama", "http://user:pass@ollama.local/v1", "pass"},
		{"anthropic", "https://key@api.anthropic.com", "key@"},
	} {
		err := ValidateAPIBase(tc.provider, tc.apiBase)
		require.Error(t, err, "%s %s", tc.provider, tc.apiBase)
		assert.NotErrorIs(t, err, ErrDisallowedAPIBase, "%s %s", tc.provider, tc.apiBase)
		assertNoAPIBaseEcho(t, err, tc.apiBase, tc.secret)
	}
}

func assertNoAPIBaseEcho(t *testing.T, err error, apiBase, secret string) {
	t.Helper()
	if apiBase != "" {
		assert.NotContains(t, err.Error(), apiBase, "the error must not echo apiBase")
	}
	if secret != "" {
		assert.NotContains(t, err.Error(), secret, "the error must not echo the credential in apiBase")
	}
}
