package matching

import "testing"

func TestLookupTeamIDByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fullName string
		want     int64
		wantErr  bool
	}{
		{"Boston Bruins", "Boston Bruins", 6, false},
		{"Montréal Canadiens with accent", "Montréal Canadiens", 8, false},
		{"Vegas Golden Knights", "Vegas Golden Knights", 54, false},
		{"unknown name", "Toronto Maple Leaves", 0, true},
		{"empty name", "", 0, true},
		{"abbrev as name", "BOS", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := LookupTeamIDByName(tc.fullName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LookupTeamIDByName(%q) err = %v, wantErr = %v", tc.fullName, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("LookupTeamIDByName(%q) = %d, want %d", tc.fullName, got, tc.want)
			}
		})
	}
}

// TestAliasAbbrevsResolveToSameID pins down the contract for alias entries:
// distinct abbreviations like CGS/CSE and CLE/CBN intentionally share an
// underlying team ID. The reverse-by-ID maps only retain the first occurrence
// (map iteration order), so this verifies forward lookup, not reverse.
func TestAliasAbbrevsResolveToSameID(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		a, b string
	}{
		{"CGS", "CSE"},
		{"CLE", "CBN"},
	}

	for _, p := range pairs {
		idA, errA := LookupTeamID(p.a)
		if errA != nil {
			t.Fatalf("LookupTeamID(%q) unexpected err: %v", p.a, errA)
		}
		idB, errB := LookupTeamID(p.b)
		if errB != nil {
			t.Fatalf("LookupTeamID(%q) unexpected err: %v", p.b, errB)
		}
		if idA != idB {
			t.Errorf("alias mismatch: %s=%d, %s=%d (expected equal)", p.a, idA, p.b, idB)
		}
	}
}
