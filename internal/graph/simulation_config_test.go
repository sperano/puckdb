package graph

import (
	"errors"
	"math"
	"testing"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/simulation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validSimPoolInput is a createSimPool input every check accepts.
func validSimPoolInput() model.CreateSimPoolInput {
	var roster []*model.SimRosterPositionInput
	for _, slot := range []simulation.RosterSlot{
		simulation.SlotC, simulation.SlotLW, simulation.SlotRW, simulation.SlotD,
		simulation.SlotUtil, simulation.SlotG, simulation.SlotBN, simulation.SlotIR,
	} {
		roster = append(roster, &model.SimRosterPositionInput{
			Slot: string(slot), Count: simulation.SupportedRosterPositions()[slot],
		})
	}
	return model.CreateSimPoolInput{
		Name:            "valid",
		Season:          helperFixtureSeason,
		Categories:      simulation.SupportedCategories(),
		RosterPositions: roster,
		WaiverDays:      2,
		DraftRounds:     2,
		Agents: []*model.CreateSimAgentInput{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", Strategy: "s"},
			{Provider: "Ollama", Model: "llama3.1:8b", Strategy: "s"},
		},
	}
}

func intPtr(n int) *int { return &n }

func strPtr(s string) *string { return &s }

// rejectedFields returns the field names of a *SimPoolConfigError.
func rejectedFields(t *testing.T, err error) []string {
	t.Helper()
	var cfgErr *SimPoolConfigError
	require.ErrorAs(t, err, &cfgErr)
	out := make([]string, len(cfgErr.Fields))
	for i, f := range cfgErr.Fields {
		out[i] = f.Field
	}
	return out
}

func TestValidateCreateSimPoolInputAcceptsTheSupportedShape(t *testing.T) {
	t.Parallel()
	settings, err := validateCreateSimPoolInput(validSimPoolInput())
	require.NoError(t, err)
	assert.Equal(t, simulation.SupportedRosterPositions(), settings.rosters)
	assert.Equal(t, simulation.StopAfterNever, settings.stopAfter, "an omitted stopAfter is never")

	in := validSimPoolInput()
	draft := "draft"
	in.StopAfter = &draft
	in.DraftRounds = simulation.RosterCapacity(simulation.SupportedRosterPositions())
	settings, err = validateCreateSimPoolInput(in)
	require.NoError(t, err)
	assert.Equal(t, simulation.StopAfterDraft, settings.stopAfter)
}

// The boundaries every range check accepts: one draft round, one agent,
// math.MaxInt32 in each int32 column, and each provider's allowed apiBase.
func TestValidateCreateSimPoolInputAcceptsTheBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*model.CreateSimPoolInput)
	}{
		{"one draft round", func(in *model.CreateSimPoolInput) { in.DraftRounds = minDraftRounds }},
		{"one agent", func(in *model.CreateSimPoolInput) { in.Agents = in.Agents[:minSimAgents] }},
		{"int32 maximum", func(in *model.CreateSimPoolInput) {
			in.Season = math.MaxInt32
			in.WaiverDays = math.MaxInt32
			in.MaxSeasonDays = intPtr(math.MaxInt32)
			in.Agents[0].TimeoutSeconds = intPtr(math.MaxInt32)
			in.Agents[0].MaxTokens = intPtr(math.MaxInt32)
		}},
		{"provider's own apiBase", func(in *model.CreateSimPoolInput) {
			in.Agents[0].APIBase = strPtr("https://api.anthropic.com")
			in.Agents = append(in.Agents, &model.CreateSimAgentInput{
				Provider: "google", Model: "gemini-2.0-flash", Strategy: "s",
				APIBase: strPtr("https://generativelanguage.googleapis.com/v1beta/openai/"),
			})
		}},
		{"any ollama apiBase", func(in *model.CreateSimPoolInput) { in.Agents[1].APIBase = strPtr("http://ollama.local:11434/v1") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := validSimPoolInput()
			tc.edit(&in)
			require.NoError(t, ValidateCreateSimPoolInput(in))
		})
	}
}

// A rejected roster has no meaningful capacity: draftRounds is then held
// only to its lower bound, so its error never quotes a wrong capacity.
func TestValidateCreateSimPoolInputChecksDraftRoundsCapacityOnlyOnAValidRoster(t *testing.T) {
	t.Parallel()
	in := validSimPoolInput()
	in.RosterPositions[6].Count = 5 // BN 5: capacity 20
	in.DraftRounds = simulation.RosterCapacity(simulation.SupportedRosterPositions())
	assert.Equal(t, []string{"rosterPositions"}, rejectedFields(t, ValidateCreateSimPoolInput(in)))

	in.DraftRounds = 0
	err := ValidateCreateSimPoolInput(in)
	assert.Equal(t, []string{"rosterPositions", "draftRounds"}, rejectedFields(t, err))
	assert.Contains(t, err.Error(), "draftRounds: 0 is below 1")
	assert.NotContains(t, err.Error(), "the roster holds")
}

func TestValidateCreateSimPoolInputRejectsEachBadField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		edit   func(*model.CreateSimPoolInput)
		field  string
		reason string
	}{
		{"blank name", func(in *model.CreateSimPoolInput) { in.Name = " " }, "name", "required"},
		{"season zero", func(in *model.CreateSimPoolInput) { in.Season = 0 }, "season", "out of range [1, "},
		{"season beyond int32", func(in *model.CreateSimPoolInput) { in.Season = math.MaxInt32 + 1 }, "season", "out of range"},
		{"seven categories", func(in *model.CreateSimPoolInput) { in.Categories = in.Categories[:7] }, "categories", `missing category "GA"`},
		{"unknown slot", func(in *model.CreateSimPoolInput) { in.RosterPositions[0].Slot = "F" }, "rosterPositions[0].slot", `unknown slot "F"`},
		{"slot listed twice", func(in *model.CreateSimPoolInput) { in.RosterPositions[1].Slot = "C" }, "rosterPositions[1].slot", `slot "C" listed twice`},
		{"negative count", func(in *model.CreateSimPoolInput) { in.RosterPositions[0].Count = -1 }, "rosterPositions[0].count", "negative"},
		{"null roster entry", func(in *model.CreateSimPoolInput) { in.RosterPositions[0] = nil }, "rosterPositions[0]", "required"},
		{"other roster", func(in *model.CreateSimPoolInput) { in.RosterPositions[6].Count = 5 }, "rosterPositions", "BN count 5, want 6"},
		{"negative waiver days", func(in *model.CreateSimPoolInput) { in.WaiverDays = -1 }, "waiverDays", "out of range"},
		{"no draft round", func(in *model.CreateSimPoolInput) { in.DraftRounds = 0 }, "draftRounds", "out of range [1, 21]"},
		{"more rounds than roster places", func(in *model.CreateSimPoolInput) { in.DraftRounds = 22 }, "draftRounds", "the roster holds 21 players"},
		{"no agent", func(in *model.CreateSimPoolInput) { in.Agents = nil }, "agents", "at least 1 agent required, got 0"},
		{"null agent", func(in *model.CreateSimPoolInput) { in.Agents[1] = nil }, "agents[1]", "required"},
		{"unknown provider", func(in *model.CreateSimPoolInput) { in.Agents[1].Provider = "claude" }, "agents[1].provider", `unknown provider "claude"`},
		{"keyed apiBase on another host", func(in *model.CreateSimPoolInput) { in.Agents[0].APIBase = strPtr("https://collector.example/v1") }, "agents[0].apiBase", "must be an https URL on api.anthropic.com"},
		{"malformed ollama apiBase", func(in *model.CreateSimPoolInput) { in.Agents[1].APIBase = strPtr("ollama.local:11434") }, "agents[1].apiBase", "must be an absolute http(s) URL"},
		{"blank model", func(in *model.CreateSimPoolInput) { in.Agents[1].Model = "" }, "agents[1].model", "required"},
		{"unpriced model", func(in *model.CreateSimPoolInput) { in.Agents[0].Model = "gpt-9-ultra-mystery" }, "agents[0].model", "no known pricing"},
		{"negative timeout", func(in *model.CreateSimPoolInput) { in.Agents[0].TimeoutSeconds = intPtr(-1) }, "agents[0].timeoutSeconds", "out of range"},
		{"max tokens beyond int32", func(in *model.CreateSimPoolInput) { in.Agents[0].MaxTokens = intPtr(math.MaxInt32 + 1) }, "agents[0].maxTokens", "out of range"},
		{"unknown stopAfter", func(in *model.CreateSimPoolInput) { s := "sometimes"; in.StopAfter = &s }, "stopAfter", `invalid stop_after "sometimes"`},
		{"negative maxSeasonDays", func(in *model.CreateSimPoolInput) { in.MaxSeasonDays = intPtr(-1) }, "maxSeasonDays", "out of range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := validSimPoolInput()
			tc.edit(&in)
			err := ValidateCreateSimPoolInput(in)
			require.Equal(t, []string{tc.field}, rejectedFields(t, err))
			assert.Contains(t, err.Error(), tc.field+": ")
			assert.Contains(t, err.Error(), tc.reason)
		})
	}
}

func TestValidateCreateSimPoolInputReportsEveryBadFieldAtOnce(t *testing.T) {
	t.Parallel()
	in := validSimPoolInput()
	in.Categories = []string{"G"}
	in.DraftRounds = 0
	in.Agents[0].Model = "gpt-9-ultra-mystery"
	stop := "later"
	in.StopAfter = &stop

	err := ValidateCreateSimPoolInput(in)
	assert.Equal(t, []string{"categories", "draftRounds", "agents[0].model", "stopAfter"}, rejectedFields(t, err))
	assert.ErrorIs(t, err, simulation.ErrUnsupportedPoolShape)
	assert.ErrorIs(t, err, simulation.ErrUnpricedModel)
	assert.Regexp(t, `^invalid sim pool config: categories: .*; draftRounds: .*; agents\[0\]\.model: .*; stopAfter: `, err.Error())
}

// An unknown provider is reported once, as a provider error, not also as
// an unpriced model.
func TestValidateCreateSimPoolInputSkipsPricingForAnUnknownProvider(t *testing.T) {
	t.Parallel()
	in := validSimPoolInput()
	in.Agents[0].Provider = "mistral"
	err := ValidateCreateSimPoolInput(in)
	assert.Equal(t, []string{"agents[0].provider"}, rejectedFields(t, err))
	assert.False(t, errors.Is(err, simulation.ErrUnpricedModel))
}
