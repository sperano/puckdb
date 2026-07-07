package simulation

import (
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/llm"
)

// ============================================================================
// Agent — wraps an llm.Client with this agent's pre-built static blocks.
// One Agent per sim_agents row; each owns its own client instance
// configured with the agent's per-call timeout and base URL.
// ============================================================================

// Agent owns everything needed to make a single decision turn for one
// fantasy manager: the LLM client (provider-routed), the cached system
// prompt (built once at construction, byte-stable for the pool's
// lifetime), and the draft + daily tool lists (also byte-stable).
//
// The struct is read-only after NewAgent returns. It can be shared
// across goroutines: each Decide-style call builds its own
// llm.Request from the cached pieces; nothing inside Agent mutates.
//
// ID is the sim_agents.id — the stable identifier used in metrics,
// logs, and error messages. Before PickTeamName completes, this is
// the agent's only name; after, displays prefer Config-side identity
// (none today) or fetch sim_agents.team_name from the DB.
type Agent struct {
	ID     int32
	Config AgentConfig
	Client llm.Client

	systemPrompt string
	draftTools   []llm.Tool
	dailyTools   []llm.Tool
}

// NewAgent builds an Agent from one AgentConfig (one sim_agents row).
//
// providerConfigs is the workspace-wide map of provider → connection
// details (keys, URLs); typically built once via
// llm.NewProviderConfigs(...) and passed in. agent.APIBase, when
// non-empty, OVERRIDES providerConfigs[provider].BaseURL — that's how
// Google Gemini works (provider routing = OpenAI-compatible, but the
// caller supplies a Google base URL).
//
// agent.TimeoutSeconds, when > 0, becomes WithTimeout on the client.
// numTeams is the pool size, baked into the cached system prompt's
// {N} placeholder.
//
// Returns an error if agent.Provider is not a recognized provider
// string, or if the resolved provider has no entry in providerConfigs.
func NewAgent(agentID int32, agent AgentConfig, providerConfigs map[llm.Provider]llm.ProviderConfig, numTeams int) (*Agent, error) {
	provider, err := resolveProvider(agent.Provider)
	if err != nil {
		return nil, err
	}

	pcfg, ok := providerConfigs[provider]
	if !ok {
		return nil, fmt.Errorf("simulation: no provider config for %s (agent #%d)", provider, agentID)
	}
	if agent.APIBase != "" {
		pcfg.BaseURL = agent.APIBase
	}

	var opts []llm.Option
	if d := agentTimeout(agent); d > 0 {
		opts = append(opts, llm.WithTimeout(d))
	}

	rawClient := llm.NewClientForProvider(provider, pcfg, agent.Model, opts...)
	// Wrap with metrics instrumentation so every Complete call
	// observes puckdb_sim_llm_call_duration_seconds +
	// puckdb_sim_llm_failures_total. Labels are baked in at
	// construction; one wrapper per (pool, agent) lifetime.
	// agentID is the stable identifier (the agent's team_name only
	// exists after Phase 0 PickTeamName completes).
	instrumented := newInstrumentedLLMClient(rawClient, agent.Provider, agent.Model, fmt.Sprintf("agent-%d", agentID))
	return &Agent{
		ID:           agentID,
		Config:       agent,
		Client:       instrumented,
		systemPrompt: BuildSystemPrompt(agent, numTeams),
		draftTools:   DraftTools(),
		dailyTools:   DailyTools(),
	}, nil
}

// SystemPrompt returns the cached system prompt string. Exposed
// primarily for tests and for cache-hit verification (the Phase 3.3
// "log cache_read_input_tokens" telemetry needs to know the prompt is
// stable across calls).
func (a *Agent) SystemPrompt() string { return a.systemPrompt }

// DraftTools returns the per-agent tool list for the draft phase.
// Returns the cached slice — callers MUST NOT mutate it.
func (a *Agent) DraftTools() []llm.Tool { return a.draftTools }

// DailyTools returns the per-agent tool list for the daily phase.
// Returns the cached slice — callers MUST NOT mutate it.
func (a *Agent) DailyTools() []llm.Tool { return a.dailyTools }

// DraftMessages assembles the [system, user] message pair for one
// draft pick. The system message carries Cacheable=true so the
// Anthropic translator places the cache_control marker on the trailing
// cacheable tool (set on agentloop.Config.Tools when calling Run).
//
// The returned slice is freshly allocated; callers may extend it.
func (a *Agent) DraftMessages(input DraftPromptInput) ([]llm.Message, error) {
	user, err := BuildDraftPrompt(input)
	if err != nil {
		return nil, err
	}
	return []llm.Message{
		{Role: "system", Content: a.systemPrompt, Cacheable: true},
		{Role: "user", Content: user},
	}, nil
}

// DailyMessages is the daily-management counterpart to DraftMessages.
// Same Cacheable contract.
func (a *Agent) DailyMessages(input DailyPromptInput) ([]llm.Message, error) {
	user, err := BuildDailyPrompt(input)
	if err != nil {
		return nil, err
	}
	return []llm.Message{
		{Role: "system", Content: a.systemPrompt, Cacheable: true},
		{Role: "user", Content: user},
	}, nil
}

// ============================================================================
// Action parsing — convert llm.ToolCall arguments into typed structs.
// ============================================================================

// Action is the parsed form of a tool call. Concrete types are the
// per-tool *Args structs in tools.go (DraftPlayerArgs, SetLineupArgs,
// AddPlayerArgs, ClaimPlayerArgs, DropPlayerArgs, UpdateNotesArgs).
//
// Activities dispatch on the concrete type via type-switch; the
// ToolName() method exists so callers that prefer name-based dispatch
// (e.g., audit logs grouped by tool) don't need to maintain their own
// type → name map.
type Action interface {
	ToolName() string
}

// Marker methods. Compiler enforces every Action implements ToolName().
func (DraftPlayerArgs) ToolName() string { return ToolDraftPlayer }
func (SetLineupArgs) ToolName() string   { return ToolSetLineup }
func (AddPlayerArgs) ToolName() string   { return ToolAddPlayer }
func (ClaimPlayerArgs) ToolName() string { return ToolClaimPlayer }
func (DropPlayerArgs) ToolName() string  { return ToolDropPlayer }
func (UpdateNotesArgs) ToolName() string { return ToolUpdateNotes }
func (SetTeamNameArgs) ToolName() string { return ToolSetTeamName }

// ParseAction converts an llm.ToolCall into the right typed Action.
// Returns an error for unknown tool names — that's a fuzzy-recovery
// signal for Phase 2.2; the daily activity should log the call as an
// error transaction rather than crash. The returned error is also
// suitable to surface back to the LLM as a tool result so the model
// can correct itself in the next round.
func ParseAction(call llm.ToolCall) (Action, error) {
	switch call.Function.Name {
	case ToolDraftPlayer:
		args, err := ParseArgs[DraftPlayerArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolSetLineup:
		args, err := ParseArgs[SetLineupArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolAddPlayer:
		args, err := ParseArgs[AddPlayerArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolClaimPlayer:
		args, err := ParseArgs[ClaimPlayerArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolDropPlayer:
		args, err := ParseArgs[DropPlayerArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolUpdateNotes:
		args, err := ParseArgs[UpdateNotesArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	case ToolSetTeamName:
		args, err := ParseArgs[SetTeamNameArgs](call)
		if err != nil {
			return nil, err
		}
		return args, nil
	default:
		return nil, fmt.Errorf("simulation: unknown tool %q", call.Function.Name)
	}
}

// ============================================================================
// Provider string parsing — keeps AgentConfig YAML-friendly.
// ============================================================================

// agentTimeout returns the per-agent HTTP timeout derived from
// AgentConfig.TimeoutSeconds, or zero if no override is configured.
// Zero means "use llm.defaultHTTPTimeout" — NewAgent skips the
// WithTimeout option in that case.
func agentTimeout(agent AgentConfig) time.Duration {
	if agent.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(agent.TimeoutSeconds) * time.Second
}

// resolveProvider turns AgentConfig.Provider (a free-form string from
// YAML config) into the llm.Provider enum.
//
// "google" / "gemini" route to ProviderOpenAI because Google's Gemini
// is reachable via an OpenAI-compatible endpoint (PLAN.md > Multi-Provider
// Support). The caller MUST set agent.APIBase to the Gemini URL — there
// is no global Gemini base in llm.NewProviderConfigs.
func resolveProvider(s string) (llm.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "anthropic":
		return llm.ProviderAnthropic, nil
	case "openai":
		return llm.ProviderOpenAI, nil
	case "ollama":
		return llm.ProviderOllama, nil
	case "google", "gemini":
		// Gemini speaks OpenAI-compatible; the per-agent APIBase override
		// in NewAgent points the request at Google's endpoint.
		return llm.ProviderOpenAI, nil
	case "":
		return 0, fmt.Errorf("simulation: agent provider is required")
	default:
		return 0, fmt.Errorf("simulation: unknown provider %q (expected anthropic, openai, ollama, google)", s)
	}
}
