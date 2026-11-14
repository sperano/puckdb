package graph

import (
	"fmt"
	"math"
	"strings"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/simulation"
)

// minSimAgents is the smallest pool createSimPool accepts: one team.
const minSimAgents = 1

// minDraftRounds is the smallest draft createSimPool accepts.
const minDraftRounds = 1

// SimPoolFieldError is one rejected createSimPool input field. Field is
// the GraphQL input path (`draftRounds`, `agents[1].provider`), which is
// also the YAML key of `puckdb sim create --config`.
type SimPoolFieldError struct {
	Field string
	Err   error
}

func (e *SimPoolFieldError) Error() string {
	return fmt.Sprintf("%s: %v", e.Field, e.Err)
}

func (e *SimPoolFieldError) Unwrap() error { return e.Err }

// SimPoolConfigError lists every rejected field of one createSimPool
// input, in input order.
type SimPoolConfigError struct {
	Fields []*SimPoolFieldError
}

func (e *SimPoolConfigError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Error()
	}
	return "invalid sim pool config: " + strings.Join(parts, "; ")
}

// Unwrap exposes each field error to errors.Is / errors.As.
func (e *SimPoolConfigError) Unwrap() []error {
	out := make([]error, len(e.Fields))
	for i, f := range e.Fields {
		out[i] = f
	}
	return out
}

// simPoolSettings is what createSimPool stores from a validated input.
type simPoolSettings struct {
	rosters   map[simulation.RosterSlot]int
	stopAfter simulation.StopAfter
}

// simPoolValidator collects field errors.
type simPoolValidator struct {
	fields []*SimPoolFieldError
}

func (v *simPoolValidator) fail(field string, err error) {
	v.fields = append(v.fields, &SimPoolFieldError{Field: field, Err: err})
}

func (v *simPoolValidator) failf(field, format string, args ...any) {
	v.fail(field, fmt.Errorf(format, args...))
}

// int32Field rejects a value below min or above the int32 column range,
// so the later int32 conversion never wraps.
func (v *simPoolValidator) int32Field(field string, value, minimum int) {
	if value < minimum || value > math.MaxInt32 {
		v.failf(field, "%d out of range [%d, %d]", value, minimum, math.MaxInt32)
	}
}

// ValidateCreateSimPoolInput checks a createSimPool input without
// touching the database: the season id and window dates are checked
// against the seasons row when the pool is created. Every rejected field
// is reported in one *SimPoolConfigError.
func ValidateCreateSimPoolInput(input model.CreateSimPoolInput) error {
	_, err := validateCreateSimPoolInput(input)
	return err
}

func validateCreateSimPoolInput(input model.CreateSimPoolInput) (simPoolSettings, error) {
	v := &simPoolValidator{}
	if strings.TrimSpace(input.Name) == "" {
		v.failf("name", "required")
	}
	v.int32Field("season", input.Season, 1)
	if err := simulation.ValidateCategories(input.Categories); err != nil {
		v.fail("categories", err)
	}
	rosters, rostersOK := v.rosterPositions(input.RosterPositions)
	v.int32Field("waiverDays", input.WaiverDays, 0)
	v.draftRounds(input.DraftRounds, rosters, rostersOK)
	v.agents(input.Agents)
	stop, err := simulation.ParseStopAfter(derefString(input.StopAfter))
	if err != nil {
		v.fail("stopAfter", err)
	}
	v.int32Field("maxSeasonDays", derefInt(input.MaxSeasonDays), 0)
	if len(v.fields) > 0 {
		return simPoolSettings{}, &SimPoolConfigError{Fields: v.fields}
	}
	return simPoolSettings{rosters: rosters, stopAfter: stop}, nil
}

// rosterPositions flattens the slot list, rejecting a null entry (a bare
// `-` in the YAML config), unknown slots and a slot listed twice (the map
// form would keep only the last count), then checks the counts against
// the supported roster. ok is false when any of these checks failed.
func (v *simPoolValidator) rosterPositions(input []*model.SimRosterPositionInput) (rosters map[simulation.RosterSlot]int, ok bool) {
	seen := make(map[simulation.RosterSlot]bool, len(input))
	valid := true
	for i, e := range input {
		field := fmt.Sprintf("rosterPositions[%d]", i)
		if e == nil {
			v.failf(field, "required")
			valid = false
			continue
		}
		slot := simulation.RosterSlot(e.Slot)
		switch {
		case !simulation.IsRosterSlot(slot):
			v.failf(field+".slot", "unknown slot %q", e.Slot)
			valid = false
		case seen[slot]:
			v.failf(field+".slot", "slot %q listed twice", e.Slot)
			valid = false
		}
		seen[slot] = true
		if e.Count < 0 {
			v.failf(field+".count", "%d is negative", e.Count)
			valid = false
		}
	}
	rosters = flattenRosterPositions(input)
	if valid {
		if err := simulation.ValidateRosterPositions(rosters); err != nil {
			v.fail("rosterPositions", err)
			valid = false
		}
	}
	return rosters, valid
}

// draftRounds must leave every drafted player a roster place: at most one
// round per roster slot of the configured roster. A rejected roster has no
// meaningful capacity, so only the lower bound is checked then.
func (v *simPoolValidator) draftRounds(rounds int, rosters map[simulation.RosterSlot]int, rostersOK bool) {
	if !rostersOK {
		if rounds < minDraftRounds {
			v.failf("draftRounds", "%d is below %d", rounds, minDraftRounds)
		}
		return
	}
	capacity := simulation.RosterCapacity(rosters)
	if rounds < minDraftRounds || rounds > capacity {
		v.failf("draftRounds", "%d out of range [%d, %d] (the roster holds %d players)",
			rounds, minDraftRounds, capacity, capacity)
	}
}

// agents checks the agent count and each agent: a null entry (a bare `-`
// in the YAML config) is rejected before any of its fields is read.
func (v *simPoolValidator) agents(agents []*model.CreateSimAgentInput) {
	if len(agents) < minSimAgents {
		v.failf("agents", "at least %d agent required, got %d", minSimAgents, len(agents))
	}
	for i, a := range agents {
		field := fmt.Sprintf("agents[%d]", i)
		if a == nil {
			v.failf(field, "required")
			continue
		}
		v.agent(field, a)
	}
}

// agent checks one agent's provider, model, apiBase and numeric
// overrides. An agent whose model has no known pricing is rejected too:
// letting it through would mean maxLlmCostUsdPerPool could never trip for
// that agent (SIM-U2). Ollama is exempt, see
// simulation.ValidateModelPricing. The provider-dependent checks are
// skipped for an unknown provider, already reported.
func (v *simPoolValidator) agent(field string, a *model.CreateSimAgentInput) {
	providerErr := simulation.ValidateProvider(a.Provider)
	if providerErr != nil {
		v.fail(field+".provider", providerErr)
	}
	switch {
	case strings.TrimSpace(a.Model) == "":
		v.failf(field+".model", "required")
	case providerErr == nil:
		if err := simulation.ValidateModelPricing(a.Provider, a.Model); err != nil {
			v.fail(field+".model", err)
		}
	}
	if providerErr == nil {
		if err := simulation.ValidateAPIBase(a.Provider, derefString(a.APIBase)); err != nil {
			v.fail(field+".apiBase", err)
		}
	}
	v.int32Field(field+".timeoutSeconds", derefInt(a.TimeoutSeconds), 0)
	v.int32Field(field+".maxTokens", derefInt(a.MaxTokens), 0)
}
