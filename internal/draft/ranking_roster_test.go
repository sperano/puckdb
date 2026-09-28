package draft

import (
	"strings"
	"testing"
)

func TestCapsFromRules_WeeklyGoalieMinimum(t *testing.T) {
	t.Parallel()
	goalies := func(count int) []RosterSlot {
		return []RosterSlot{{Position: PositionGoalie, Count: count, Starting: true}}
	}
	tests := []struct {
		name    string
		scoring string
		slots   []RosterSlot
		value   string
		wantErr string
	}{
		{name: "head non-binding", scoring: scoringTypeHead, slots: goalies(2), value: "3"},
		{name: "head binding", scoring: scoringTypeHead, slots: goalies(1), value: "3", wantErr: "is binding"},
		{name: "head invalid", scoring: scoringTypeHead, slots: goalies(2), value: "abc", wantErr: "positive number"},
		{name: "head zero", scoring: scoringTypeHead, slots: goalies(2), value: "0", wantErr: "positive number"},
		{name: "roto stays unmodeled", scoring: "roto", slots: goalies(2), value: "3", wantErr: "unmodeled workload limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rules := Rules{
				ScoringType: tt.scoring,
				RosterSlots: tt.slots,
				Settings:    map[string]string{weeklyGoalieMinimumSetting: tt.value},
			}
			caps, err := capsFromRules(rules)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if caps != (rankingCaps{}) {
					t.Fatalf("minimum must not set caps: %+v", caps)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
