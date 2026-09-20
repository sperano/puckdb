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

func TestLookupTeamIDForSeason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		abbrev  string
		season  int
		want    int64
		wantErr bool
	}{
		{"Utah Hockey Club season", "UTA", 20242025, 59, false},
		{"first Utah Mammoth season", "UTA", utahMammothFirstSeason, 68, false},
		{"later Utah Mammoth season", "UTA", 20262027, 68, false},
		{"abbrev without eras", "SJS", 20252026, 28, false},
		{"unknown abbrev", "XXX", 20252026, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := LookupTeamIDForSeason(tc.abbrev, tc.season)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LookupTeamIDForSeason(%q, %d) err = %v, wantErr = %v", tc.abbrev, tc.season, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("LookupTeamIDForSeason(%q, %d) = %d, want %d", tc.abbrev, tc.season, got, tc.want)
			}
		})
	}
}

// TestTeamErasHaveReverseLookups guards the player-landing path, which
// resolves season totals by full team name: an era team missing from the
// reverse maps would leave its player_season_totals rows with a NULL team_id.
func TestTeamErasHaveReverseLookups(t *testing.T) {
	t.Parallel()

	for abbrev, eras := range teamErasByAbbrev {
		for _, era := range eras {
			id, err := LookupTeamIDByName(era.info.FullName)
			if err != nil || id != era.info.ID {
				t.Errorf("LookupTeamIDByName(%q) = %d, %v; want %d", era.info.FullName, id, err, era.info.ID)
			}
			got, err := LookupTeamAbbrev(era.info.ID)
			if err != nil || got != abbrev {
				t.Errorf("LookupTeamAbbrev(%d) = %q, %v; want %q", era.info.ID, got, err, abbrev)
			}
		}
	}
}

// TestTeamErasAscending pins the ordering LookupTeamIDForSeason relies on:
// it scans eras newest-first, so an out-of-order entry would shadow a
// later era.
func TestTeamErasAscending(t *testing.T) {
	t.Parallel()

	for abbrev, eras := range teamErasByAbbrev {
		for i := 1; i < len(eras); i++ {
			if eras[i].firstSeason <= eras[i-1].firstSeason {
				t.Errorf("teamErasByAbbrev[%q][%d].firstSeason = %d, want > %d", abbrev, i, eras[i].firstSeason, eras[i-1].firstSeason)
			}
		}
	}
}
