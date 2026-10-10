package simulation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sperano/puckdb/internal/llm"
)

// ============================================================================
// Pricing table — reviewed quarterly.
//
//	Last review: 2026-Q2 (rev3 — restore claude-sonnet-4-7, the CLI help's
//	documented example model (cmd/sim.go); normalize the "gemini" provider
//	alias to "google" before lookup; ValidateModelPricing now rejects a
//	non-Ollama agent whose (provider, model) isn't in this table, instead
//	of letting it through as a free model — see SIM-U2)
//	Sources:
//	  - https://www.anthropic.com/pricing       (Anthropic Claude family)
//	  - https://openai.com/api/pricing/         (OpenAI GPT family)
//	  - https://ai.google.dev/pricing           (Google Gemini family)
//
// All rates are USD per ONE MILLION tokens.
//
// Cache rates apply only to providers that surface
// `cache_creation_input_tokens` and `cache_read_input_tokens` in their
// usage payload — currently Anthropic only. For other providers those
// fields stay zero (see the OpenAI client in llm/client.go) and the
// cache columns in this table are zero too, so they simply don't
// contribute to ComputeCost.
//
// ComputeCost and EstimateCost still return $0 for a (provider, model)
// pair that isn't in this table — that is what lets Ollama's arbitrary,
// locally-pulled model tags run free without a table entry for each one.
// But nothing except Ollama is allowed to reach that $0 branch: pool
// creation runs every agent through ValidateModelPricing, which rejects
// a non-Ollama provider/model pair that isn't priced here. Add a row (and
// bump "Last review") instead of relying on the zero-cost fallback for a
// real model.
// ============================================================================

// Provider strings used as the first half of the pricing key. Match the
// AgentConfig.Provider conventions; pricing lookups lowercase the input
// so callers don't have to think about casing.
const (
	PricingProviderAnthropic = "anthropic"
	PricingProviderOpenAI    = "openai"
	PricingProviderGoogle    = "google"
	PricingProviderOllama    = "ollama"
)

// pricePerMTokens is the unit divisor applied at the end of ComputeCost.
// Named so the formula reads as "(input + cache + output) / per-million".
const pricePerMTokens = 1_000_000

// Pricing carries the four token rates for one (provider, model) pair.
// All zero values are meaningful: a $0 rate simply means that token
// category doesn't contribute to cost (e.g., OpenAI has no
// CacheCreation rate because the OpenAI usage payload doesn't surface
// cache-creation tokens).
type Pricing struct {
	// Input is the rate for billable input tokens (excludes cached and
	// cache-creation tokens, matching Anthropic's accounting).
	Input float64
	// CacheCreation is the rate for tokens written into the prompt cache.
	// Anthropic's ephemeral cache bills these at ~1.25x the Input rate.
	// Zero means the provider doesn't bill a separate cache-creation
	// category (OpenAI, Google, Ollama).
	CacheCreation float64
	// CacheRead is the rate for tokens read from the prompt cache.
	// Anthropic's ephemeral cache bills these at ~0.10x the Input rate.
	CacheRead float64
	// Output is the rate for completion tokens.
	Output float64
}

type pricingKey struct {
	provider string
	model    string
}

// pricingTable is keyed by (provider, model). Lookups are exact match
// AFTER lowercasing both halves of the key — see LookupPricing.
//
// Adding a model: copy a row, update name + rates from the provider's
// public pricing page. Bump the "Last review" date in the file header
// and verify the table-driven tests still pass.
var pricingTable = map[pricingKey]Pricing{
	// ---- Anthropic Claude (5-min ephemeral cache) ----
	// Opus 4.x: $5/$25 input/output; cache write 1.25× input ($6.25), cache read 0.10× input ($0.50).
	{PricingProviderAnthropic, "claude-opus-4-8"}: {Input: 5.00, CacheCreation: 6.25, CacheRead: 0.50, Output: 25.00},
	{PricingProviderAnthropic, "claude-opus-4-7"}: {Input: 5.00, CacheCreation: 6.25, CacheRead: 0.50, Output: 25.00},
	{PricingProviderAnthropic, "claude-opus-4-6"}: {Input: 5.00, CacheCreation: 6.25, CacheRead: 0.50, Output: 25.00},
	// Sonnet 4.x: $3/$15 input/output; cache write 1.25× input ($3.75), cache read 0.10× input ($0.30).
	{PricingProviderAnthropic, "claude-sonnet-4-6"}: {Input: 3.00, CacheCreation: 3.75, CacheRead: 0.30, Output: 15.00},
	{PricingProviderAnthropic, "claude-sonnet-4-7"}: {Input: 3.00, CacheCreation: 3.75, CacheRead: 0.30, Output: 15.00},
	{PricingProviderAnthropic, "claude-haiku-4-5"}:  {Input: 1.00, CacheCreation: 1.25, CacheRead: 0.10, Output: 5.00},

	// ---- OpenAI ----
	// OpenAI does automatic caching as of GPT-4o; the discount surfaces in
	// `prompt_tokens_details.cached_tokens` rather than the cache_creation
	// / cache_read split that Anthropic uses. Until our llm.Usage carries
	// that field, OpenAI cache discounts aren't accounted for here.
	{PricingProviderOpenAI, "gpt-4o"}:      {Input: 2.50, Output: 10.00},
	{PricingProviderOpenAI, "gpt-4o-mini"}: {Input: 0.15, Output: 0.60},

	// ---- Google Gemini ----
	{PricingProviderGoogle, "gemini-2.0-flash"}: {Input: 0.075, Output: 0.30},
	{PricingProviderGoogle, "gemini-1.5-flash"}: {Input: 0.075, Output: 0.30},
}

// pricingProviderAliases maps a free-form provider spelling to the
// canonical provider half of a pricingKey. "gemini" is the model
// family name Google documents; resolveProvider (agent.go) already
// treats "google" and "gemini" as the same LLM transport, so pricing
// must resolve them to the same table rows instead of only recognizing
// "google".
var pricingProviderAliases = map[string]string{
	"gemini": PricingProviderGoogle,
}

// canonicalPricingProvider lowercases provider and resolves it through
// pricingProviderAliases, so both halves of pricingKey construction
// agree on one spelling per provider family.
func canonicalPricingProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if canon, ok := pricingProviderAliases[p]; ok {
		return canon
	}
	return p
}

// dateSuffixRE matches a trailing "-YYYYMMDD" component that Anthropic
// appends to model IDs in some SDK constants and API responses (e.g.
// "claude-haiku-4-5-20251001"). The eight-digit group must be at the
// end of the string.
var dateSuffixRE = regexp.MustCompile(`-\d{8}$`)

// stripDateSuffix removes a trailing "-YYYYMMDD" suffix from model, if
// present. This lets callers pass dated model IDs such as
// "claude-haiku-4-5-20251001" and still hit the table entry for
// "claude-haiku-4-5".
func stripDateSuffix(model string) string {
	return dateSuffixRE.ReplaceAllString(model, "")
}

// LookupPricing returns the rate row for (provider, model) and a found flag.
// provider is lowercased and resolved through pricingProviderAliases (so
// "gemini" hits the "google" rows); model is lowercased and has a trailing
// "-YYYYMMDD" date suffix stripped before the lookup so that dated variants
// such as "claude-haiku-4-5-20251001" resolve to the correct row. A miss
// returns the zero Pricing{} (which implies $0 cost — see the file header
// for why, and ValidateModelPricing for who is actually allowed to hit it).
func LookupPricing(provider, model string) (Pricing, bool) {
	key := pricingKey{
		provider: canonicalPricingProvider(provider),
		model:    stripDateSuffix(strings.ToLower(model)),
	}
	p, ok := pricingTable[key]
	return p, ok
}

// ErrUnpricedModel is returned by ValidateModelPricing when a non-Ollama
// agent's (provider, model) pair has no entry in pricingTable. Without
// this check, EstimateCost would silently cost such a model at $0 forever
// and maxLlmCostUsdPerPool could never trip for it (SIM-U2).
var ErrUnpricedModel = errors.New("simulation: model has no known pricing")

// ValidateModelPricing rejects an agent's (provider, model) pair unless it
// is either priced in pricingTable or explicitly exempt.
//
// Ollama is exempt unconditionally, not just when its model happens to be
// unpriced: it is the local/self-hosted provider, its models are free by
// construction, and callers pull arbitrary tags (e.g. "llama3.1:8b") that
// a static table could never fully enumerate. Every other provider —
// including a "google"/"gemini" model reached over an OpenAI-compatible
// endpoint — must resolve through LookupPricing; an unpriced paid model
// would otherwise look free and never trip the pool's cost cap.
func ValidateModelPricing(provider, model string) error {
	if canonicalPricingProvider(provider) == PricingProviderOllama {
		return nil
	}
	if _, ok := LookupPricing(provider, model); !ok {
		return fmt.Errorf("%w: provider %q model %q", ErrUnpricedModel, provider, model)
	}
	return nil
}

// ComputeCost returns the dollar cost of a single LLM call, given the
// provider/model pricing and the response usage. Returns 0 for unknown
// models (zero-value Pricing) — that's the Ollama / unknown-model
// branch.
//
// Formula: (input * Input + cache_creation * CacheCreation +
//
//	cache_read * CacheRead + output * Output) / 1_000_000.
//
// All four token counts are summed independently — they do NOT overlap
// in Anthropic's accounting (input_tokens excludes cached and
// cache-creation tokens). For providers that don't surface cache
// fields, those counts are zero and the cache terms drop out.
func ComputeCost(p Pricing, usage llm.Usage) float64 {
	totalRateTokens := float64(usage.PromptTokens)*p.Input +
		float64(usage.CacheCreationInputTokens)*p.CacheCreation +
		float64(usage.CacheReadInputTokens)*p.CacheRead +
		float64(usage.CompletionTokens)*p.Output
	return totalRateTokens / pricePerMTokens
}

// EstimateCost composes LookupPricing + ComputeCost for the common case.
// An unpriced (provider, model) pair returns 0 with no error; callers
// reach this only for Ollama, since ValidateModelPricing refuses any
// other unpriced pair at pool creation.
func EstimateCost(provider, model string, usage llm.Usage) float64 {
	p, _ := LookupPricing(provider, model)
	return ComputeCost(p, usage)
}
