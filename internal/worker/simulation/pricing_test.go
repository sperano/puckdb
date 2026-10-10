package simulation

import (
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin the headline rate for the "default sim model" assumption in
// PLAN.md > Cost Estimate (Sonnet ~$3/M in, $15/M out, $0.30/M cache
// read). If a quarterly review changes Anthropic's rates, this test
// fails — that is the *signal* to update the table, not a bug.
func TestLookupPricing_AnthropicSonnet46(t *testing.T) {
	p, ok := LookupPricing(PricingProviderAnthropic, "claude-sonnet-4-6")
	require.True(t, ok)
	assert.InDelta(t, 3.00, p.Input, 0)
	assert.InDelta(t, 15.00, p.Output, 0)
	assert.InDelta(t, 0.30, p.CacheRead, 0)
	assert.InDelta(t, 3.75, p.CacheCreation, 0,
		"Anthropic ephemeral cache writes are billed at 1.25x the input rate")
}

// Case-insensitive lookup so a YAML config that writes "Anthropic" or
// "ANTHROPIC" doesn't quietly fall through to the unknown-model branch
// and disable the cost cap.
func TestLookupPricing_CaseInsensitive(t *testing.T) {
	cases := []struct{ provider, model string }{
		{"Anthropic", "Claude-Sonnet-4-6"},
		{"ANTHROPIC", "CLAUDE-SONNET-4-6"},
		{"anthropic", "claude-sonnet-4-6"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			p, ok := LookupPricing(tc.provider, tc.model)
			require.True(t, ok)
			assert.InDelta(t, 3.00, p.Input, 0)
		})
	}
}

func TestLookupPricing_UnknownModelReturnsZero(t *testing.T) {
	p, ok := LookupPricing(PricingProviderAnthropic, "claude-imaginary-99")
	assert.False(t, ok)
	assert.Equal(t, Pricing{}, p, "unknown models must return the zero Pricing — drives the $0 / Ollama branch in ComputeCost")
}

func TestLookupPricing_OllamaIsAlwaysFree(t *testing.T) {
	// We don't seed Ollama models in the table; the Ollama provider
	// itself isn't a key. Local models flow through the unknown branch.
	p, ok := LookupPricing(PricingProviderOllama, "llama3.1:8b")
	assert.False(t, ok)
	assert.Equal(t, Pricing{}, p)
}

// "gemini" is the provider spelling Google's own docs use, and
// resolveProvider (agent.go) already accepts it as a synonym for
// "google" when picking the LLM transport. Pricing must agree, or an
// agent configured with provider: gemini would look unpriced and get
// rejected (or, before this fix, silently cost $0) despite "google"
// having real rows in the table.
func TestLookupPricing_GeminiAliasesToGoogle(t *testing.T) {
	want, ok := LookupPricing(PricingProviderGoogle, "gemini-2.0-flash")
	require.True(t, ok)
	got, ok := LookupPricing("gemini", "gemini-2.0-flash")
	require.True(t, ok, "the gemini provider alias must resolve to the google pricing rows")
	assert.Equal(t, want, got)
}

func TestLookupPricing_GeminiAliasIsCaseInsensitive(t *testing.T) {
	_, ok := LookupPricing("GEMINI", "gemini-1.5-flash")
	assert.True(t, ok)
}

// claude-sonnet-4-7 is the model the CLI's own `sim create` help text
// documents as its first example agent (cmd/sim.go). It must stay
// priced, or following that example produces an agent that trips
// ValidateModelPricing's reject path.
func TestLookupPricing_AnthropicSonnet47(t *testing.T) {
	p, ok := LookupPricing(PricingProviderAnthropic, "claude-sonnet-4-7")
	require.True(t, ok, "claude-sonnet-4-7 (the CLI's documented example model) must be priced")
	assert.InDelta(t, 3.00, p.Input, 0)
	assert.InDelta(t, 15.00, p.Output, 0)
}

// Minimal ComputeCost — Sonnet, no caching. Pin the per-million math so
// a future "let's normalize tokens to thousands" refactor can't silently
// move the decimal.
func TestComputeCost_SonnetWithoutCache(t *testing.T) {
	p, _ := LookupPricing(PricingProviderAnthropic, "claude-sonnet-4-6")
	usage := llm.Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 100_000,
	}
	cost := ComputeCost(p, usage)
	// 1M * $3 + 100K * $15 = $3.00 + $1.50 = $4.50
	assert.InDelta(t, 4.50, cost, 1e-9)
}

// Cache-read discount: a call with most input served from cache should
// cost dramatically less than the same call un-cached. This is the
// invariant PLAN.md is asking for ("~30–40% net savings on input
// cost"). Magic numbers are the four rates from the table — using them
// as named values would just be aliasing.
func TestComputeCost_SonnetCacheReadDiscount(t *testing.T) {
	p, _ := LookupPricing(PricingProviderAnthropic, "claude-sonnet-4-6")
	// 1M tokens of "system + tools" prefix served from cache; 50K tokens
	// of fresh user/assistant turn; 5K tokens of completion.
	cached := llm.Usage{
		PromptTokens:         50_000,
		CacheReadInputTokens: 1_000_000,
		CompletionTokens:     5_000,
	}
	uncached := llm.Usage{
		PromptTokens:     1_050_000, // same total, all uncached
		CompletionTokens: 5_000,
	}

	cachedCost := ComputeCost(p, cached)
	uncachedCost := ComputeCost(p, uncached)

	// Cached: 50K * $3 + 1M * $0.30 + 5K * $15 = $0.15 + $0.30 + $0.075 = $0.525
	// Uncached: 1.05M * $3 + 5K * $15 = $3.15 + $0.075 = $3.225
	assert.InDelta(t, 0.525, cachedCost, 1e-9)
	assert.InDelta(t, 3.225, uncachedCost, 1e-9)
	assert.Less(t, cachedCost, uncachedCost*0.20,
		"cache-read should drop input cost by ~10x — sanity check on the rate ratio")
}

// Cache CREATION (first call that warms the cache) bills at 1.25x the
// input rate. Pin this so a "simplify to one input rate" refactor can't
// silently undercount the warm-up cost.
func TestComputeCost_SonnetCacheCreationPremium(t *testing.T) {
	p, _ := LookupPricing(PricingProviderAnthropic, "claude-sonnet-4-6")
	usage := llm.Usage{
		CacheCreationInputTokens: 1_000_000,
	}
	cost := ComputeCost(p, usage)
	// 1M * $3.75 = $3.75
	assert.InDelta(t, 3.75, cost, 1e-9)
}

// Unknown model: zero Pricing, zero cost regardless of usage.
func TestComputeCost_UnknownModelIsFree(t *testing.T) {
	p, _ := LookupPricing(PricingProviderOllama, "llama3.1:8b")
	usage := llm.Usage{
		PromptTokens:             10_000_000,
		CompletionTokens:         5_000_000,
		CacheCreationInputTokens: 1_000_000,
		CacheReadInputTokens:     1_000_000,
	}
	assert.Equal(t, 0.0, ComputeCost(p, usage))
}

// EstimateCost is the convenience composition. Pin both branches:
// known model bills, unknown model returns 0.
func TestEstimateCost(t *testing.T) {
	t.Run("known model", func(t *testing.T) {
		usage := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 100_000}
		assert.InDelta(t, 4.50, EstimateCost(PricingProviderAnthropic, "claude-sonnet-4-6", usage), 1e-9)
	})
	t.Run("unknown model is free", func(t *testing.T) {
		usage := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 100_000}
		assert.Equal(t, 0.0, EstimateCost(PricingProviderOllama, "llama3.1:8b", usage))
	})
}

// ============================================================================
// ValidateModelPricing — the pool-creation gate (SIM-U2).
// ============================================================================

// A paid, non-Ollama provider with a model absent from pricingTable must
// be rejected: letting it through is exactly the "unknown model is free"
// hole that let maxLlmCostUsdPerPool never trip.
func TestValidateModelPricing_RejectsUnpricedPaidModel(t *testing.T) {
	err := ValidateModelPricing(PricingProviderAnthropic, "claude-imaginary-99")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnpricedModel)
}

func TestValidateModelPricing_RejectsUnpricedOpenAIModel(t *testing.T) {
	err := ValidateModelPricing(PricingProviderOpenAI, "gpt-9-ultra-mystery")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnpricedModel)
}

// Every model actually seeded in the table — including the CLI's
// documented claude-sonnet-4-7 — must validate clean.
func TestValidateModelPricing_AcceptsSupportedModels(t *testing.T) {
	cases := []struct{ provider, model string }{
		{PricingProviderAnthropic, "claude-sonnet-4-6"},
		{PricingProviderAnthropic, "claude-sonnet-4-7"},
		{PricingProviderAnthropic, "claude-opus-4-8"},
		{PricingProviderAnthropic, "claude-haiku-4-5"},
		{PricingProviderOpenAI, "gpt-4o"},
		{PricingProviderGoogle, "gemini-2.0-flash"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			assert.NoError(t, ValidateModelPricing(tc.provider, tc.model))
		})
	}
}

// The "gemini" provider alias must validate against the same rows as
// "google" rather than being rejected as unpriced.
func TestValidateModelPricing_AcceptsGeminiProviderAlias(t *testing.T) {
	assert.NoError(t, ValidateModelPricing("gemini", "gemini-2.0-flash"))
}

// A dated Anthropic model ID must validate against its base row via
// stripDateSuffix, the same as LookupPricing.
func TestValidateModelPricing_AcceptsDatedModelID(t *testing.T) {
	assert.NoError(t, ValidateModelPricing(PricingProviderAnthropic, "claude-haiku-4-5-20251001"))
}

// Ollama is explicitly local: it must be accepted regardless of the
// model tag, including one nobody could seed in advance, so self-hosted
// users aren't forced to pre-register every model they pull.
func TestValidateModelPricing_AcceptsAnyOllamaModel(t *testing.T) {
	cases := []string{"llama3.1:8b", "some-custom-finetune:latest", ""}
	for _, model := range cases {
		t.Run(model, func(t *testing.T) {
			assert.NoError(t, ValidateModelPricing(PricingProviderOllama, model))
		})
	}
}

func TestValidateModelPricing_OllamaExemptionIsCaseInsensitive(t *testing.T) {
	assert.NoError(t, ValidateModelPricing("OLLAMA", "llama3.1:8b"))
}

// Table-driven smoke pass: every seeded row has positive Input and
// Output rates. Cache fields are conditionally non-zero (Anthropic
// only). A row added with a typo'd zero rate would silently disable
// cost accounting — fail loudly here instead.
func TestPricingTable_AllRowsHavePositiveBaseRates(t *testing.T) {
	require.NotEmpty(t, pricingTable)
	for key, p := range pricingTable {
		t.Run(key.provider+"/"+key.model, func(t *testing.T) {
			assert.Greater(t, p.Input, 0.0, "Input rate must be positive")
			assert.Greater(t, p.Output, 0.0, "Output rate must be positive")
			if key.provider == PricingProviderAnthropic {
				assert.Greater(t, p.CacheCreation, 0.0, "Anthropic models must have a CacheCreation rate")
				assert.Greater(t, p.CacheRead, 0.0, "Anthropic models must have a CacheRead rate")
				assert.Less(t, p.CacheRead, p.Input, "CacheRead must be cheaper than Input — that's the whole point")
				assert.Greater(t, p.CacheCreation, p.Input, "CacheCreation must be more expensive than Input — Anthropic charges a write premium")
			}
		})
	}
}

// Pin the Opus 4.x rates at $5/$25 (input/output) with the standard Anthropic
// ephemeral-cache ratios. These were historically miscoded as $15/$75 — this
// test locks in the corrected values so a copy-paste error from an old row
// can't slip through code review.
func TestLookupPricing_AnthropicOpusRates(t *testing.T) {
	const (
		wantInput         = 5.00
		wantOutput        = 25.00
		wantCacheCreation = 6.25 // 1.25× input
		wantCacheRead     = 0.50 // 0.10× input
	)
	models := []string{"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8"}
	for _, m := range models {
		t.Run(m, func(t *testing.T) {
			p, ok := LookupPricing(PricingProviderAnthropic, m)
			require.True(t, ok)
			assert.InDelta(t, wantInput, p.Input, 0, "Input rate")
			assert.InDelta(t, wantOutput, p.Output, 0, "Output rate")
			assert.InDelta(t, wantCacheCreation, p.CacheCreation, 0, "CacheCreation rate")
			assert.InDelta(t, wantCacheRead, p.CacheRead, 0, "CacheRead rate")
		})
	}
}

// LookupPricing must resolve dated model IDs (e.g. "claude-haiku-4-5-20251001")
// to their base table entry. If it doesn't, every call with a dated ID silently
// bills $0 and the cost cap never fires.
func TestLookupPricing_DateSuffixStripped(t *testing.T) {
	cases := []struct {
		dated    string
		base     string
		provider string
	}{
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5", PricingProviderAnthropic},
		{"claude-sonnet-4-6-20260101", "claude-sonnet-4-6", PricingProviderAnthropic},
		{"claude-opus-4-8-20261231", "claude-opus-4-8", PricingProviderAnthropic},
	}
	for _, tc := range cases {
		t.Run(tc.dated, func(t *testing.T) {
			pDated, okDated := LookupPricing(tc.provider, tc.dated)
			pBase, okBase := LookupPricing(tc.provider, tc.base)
			require.True(t, okBase, "base model must be in the table")
			require.True(t, okDated, "dated variant must resolve via stripDateSuffix")
			assert.Equal(t, pBase, pDated, "dated and base lookups must return identical pricing")
		})
	}
}

// stripDateSuffix must not mangle IDs that happen to end with digits but are
// not date suffixes (e.g. a hypothetical "gpt-4o-mini-v2" — only 8-digit
// trailing groups are stripped).
func TestStripDateSuffix(t *testing.T) {
	cases := []struct{ input, want string }{
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6"}, // no suffix — unchanged
		{"gpt-4o-mini", "gpt-4o-mini"},             // no suffix — unchanged
		{"model-123", "model-123"},                 // only 3 digits — unchanged
		{"model-12345678", "model"},                // exactly 8 digits — stripped
		{"model-123456789", "model-123456789"},     // 9 digits — unchanged
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, stripDateSuffix(tc.input))
		})
	}
}

// All keys in the table must already be lowercase so callers don't have
// to second-guess the case of their seeded rows. (LookupPricing
// lowercases the input; if the table itself had a capitalized key, the
// row would be unreachable.)
func TestPricingTable_AllKeysAreLowercase(t *testing.T) {
	for key := range pricingTable {
		assert.Equal(t, key.provider, lower(key.provider), "provider key must be lowercase")
		assert.Equal(t, key.model, lower(key.model), "model key must be lowercase")
	}
}

// lower is a tiny test helper to avoid importing strings here just to
// call ToLower on string literals — the table is small and the import
// would be unused otherwise.
func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
