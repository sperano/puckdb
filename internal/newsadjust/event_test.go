package newsadjust

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvent_Validate(t *testing.T) {
	valid := testEvent("e", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	require.NoError(t, valid.Validate())
	newTeam := int64(3)

	cases := map[string]func(*Event){
		"missing version":            func(e *Event) { e.Version = 0 },
		"unknown type":               func(e *Event) { e.Type = "fight" },
		"unknown status":             func(e *Event) { e.Status = "maybe" },
		"no evidence":                func(e *Event) { e.Evidence = nil },
		"evidence without kind":      func(e *Event) { e.Evidence[0].Kind = "blog" },
		"invented game count":        func(e *Event) { e.Duration.Games = 5 },
		"games without count":        func(e *Event) { e.Duration = Duration{Kind: DurationGames} },
		"until without end":          func(e *Event) { e.Duration = Duration{Kind: DurationUntil} },
		"resolved without end":       func(e *Event) { e.Lifecycle = LifecycleResolved },
		"end before start":           func(e *Event) { e.EffectiveUntil = e.EffectiveFrom.Add(-time.Hour) },
		"supersedes itself":          func(e *Event) { e.Supersedes = []string{e.ID} },
		"trade without team":         func(e *Event) { e.Type, e.Role = EventTrade, &RoleChange{IceTime: DirectionUp} },
		"role change with no change": func(e *Event) { e.Type, e.Role = EventRoleChange, &RoleChange{} },
		"unknown role value": func(e *Event) {
			e.Type, e.Role = EventTrade, &RoleChange{TeamID: &newTeam, GoalieRole: "third"}
		},
	}
	for name, mutate := range cases {
		e := valid
		e.Evidence = append([]EvidenceRef(nil), valid.Evidence...)
		mutate(&e)
		assert.Error(t, e.Validate(), name)
	}
}

func TestEvent_AuthorityPrefersOfficialEvidence(t *testing.T) {
	e := testEvent("e", testGoalieKey, EventInjury, Duration{Kind: DurationUnknown})
	official := e.authority()
	e.Evidence[0].Kind = "reporting"
	assert.Less(t, official, e.authority())
}

func TestEvent_ValidateAcceptsReportedHeldAndEarlyResolvedEvents(t *testing.T) {
	reported := testEvent("e", testGoalieKey, EventInjury, Duration{Kind: DurationUnknown})
	reported.Status = StatusReported
	assert.NoError(t, reported.Validate())

	held := testEvent("e", testGoalieKey, EventRoleChange, Duration{Kind: DurationUnknown})
	held.Hold = "no numeric mapping"
	assert.NoError(t, held.Validate(), "a held event only alerts, so its effect is not checked")
	held.Evidence = nil
	assert.Error(t, held.Validate(), "a held event still needs its identity and evidence")

	early := testEvent("e", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	early.Lifecycle, early.EffectiveUntil = LifecycleResolved, early.EffectiveFrom.Add(-time.Hour)
	assert.NoError(t, early.Validate(), "a return reported before the start resolves the event")
}
