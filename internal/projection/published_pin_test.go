package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	pinTargetSeason = 20252026
	pinStarterID    = 8_001
	pinBackupID     = 8_002
	pinSkaterID     = 8_003
)

// pinnedHashes are the identities a published model version computed for
// pinnedInput before nhl-baseline-v6 added goalie appearances. They must never
// change: stored snapshots and evaluations are looked up and verified by them.
type pinnedHashes struct {
	config     string
	source     string
	evaluation string
	players    string
	evaluated  string
}

var publishedPins = map[string]pinnedHashes{
	LegacyModelVersion: {
		config:     "f65181e5f3856e50f4f71442d0aef10f2c69d37daf77f96476d1a3c81eebdcc6",
		source:     "838b7587a64b84bf402eb757f8e0e7b232084879b219df00f393b4e42d76032e",
		evaluation: "93cb839337dd23c49587a5425caff4079e09575912ceb0e38c7dc0d21198e432",
		players:    "ad3465ba7e7c156def42b4a1f4902c6c9af373cedb5d0a5874b4debc7a365123",
		evaluated:  "e445bf437834967d977f927193faa8f180a95090501ed51b702979cdb3121a39",
	},
	FaceoffModelVersion: {
		config:     "bdddb53fe417d350d27bc40bd6aba04188b752e5a5c74c1ae021bafab4567870",
		source:     "b43cb34295f4391dc406da26fc208adf7bcee7bbef821a267df95e05136fa14b",
		evaluation: "502304c9615559a8638152ce23c0baab46f06168d760b5d3a004144010b6ea6f",
		players:    "4769257e0969147fc5d3cba8ea3e132fc0f077ae63c046470a0f3ce7dc25b0e4",
		evaluated:  "6fb1094f53260ee70dcd37bc77953c5a3729e9f520c52d94e281b758f63a97cf",
	},
	LinemateModelVersion: {
		config:     "6501fc75eacb50e375a1d4c63aa196e886c47d6b8cf990142674e9336d26a387",
		source:     "39720d261a624def4845916cdc49ff947052ec4236c6ce66326559403c707dc0",
		evaluation: "a1a875fa41ce2c235136208ca07f7dd35060fbacdf7b0bd21626b32334d2db49",
		players:    "4769257e0969147fc5d3cba8ea3e132fc0f077ae63c046470a0f3ce7dc25b0e4",
		evaluated:  "6fa27b7081e2f5b39a5bef1ebbbd10cf64a9d19052433dadb674d4bca2bf9e89",
	},
	AgingModelVersion: {
		config:     "be98d03df5b0e72cf6ca07e2c0308b11634014e6e9ad34ada9e2bfc2674ebbb6",
		source:     "6a4f16e9b206e4c2e8e651526a8f202208a3e11e5dc988a88f5cd305ce59084e",
		evaluation: "780b7d3d5135e9f0fd42a5234a2fe704218906cfa24029212ea8632a0b497e96",
		players:    "4769257e0969147fc5d3cba8ea3e132fc0f077ae63c046470a0f3ce7dc25b0e4",
		evaluated:  "a9026db8017eb822c61f28cbf7cf450ef611869453df9292db1ca65158430ac6",
	},
	TeamEnvironmentModelVersion: {
		config:     "fbfa9a9840b9fdee3b4a6c65b82c17e9a5c3b2613d957b86ead706024ec2e56b",
		source:     "6a4f16e9b206e4c2e8e651526a8f202208a3e11e5dc988a88f5cd305ce59084e",
		evaluation: "780b7d3d5135e9f0fd42a5234a2fe704218906cfa24029212ea8632a0b497e96",
		players:    "4769257e0969147fc5d3cba8ea3e132fc0f077ae63c046470a0f3ce7dc25b0e4",
		evaluated:  "55764c713b3ace2eb122ffe3c76fe7c0908022b299903940d9c4c9c9fa68f736",
	},
	// Captured before nhl-baseline-v7 added the goalie start share.
	GoalieAppearancesModelVersion: {
		config:     "a277dade264bc578d8e2fcb23835f5fd87e29cb8b7ce72541fd496c6aff005b2",
		source:     "72efb764d4666fe93c985b5def82181f97ee28993db8ae28c7eb141b6ec068a6",
		evaluation: "c5594953513c0717518e984d5c545420a4d6808556352c75f531c886af4ddcb0",
		players:    "a8087c53934c842daaa22a13e588e1fdbdc9d997d796de9f8c8f4980352c9daf",
		evaluated:  "39bf9443b6632aa5262af24aec5954d03045adf97c01e4720202290cf8ea0a70",
	},
}

// TestPublishedVersionsKeepHashesAndOutputs pins every published version's
// config hash, source-data hash, evaluation-data hash, generated players and
// evaluation metrics on an input whose goalies dressed as the backup in many
// games they never played. nhl-baseline-v6 reads those appearances; earlier
// versions must not see them in any identity or output.
func TestPublishedVersionsKeepHashesAndOutputs(t *testing.T) {
	t.Parallel()

	for version, want := range publishedPins {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, computePins(t, version))
		})
	}
}

func computePins(t *testing.T, version string) pinnedHashes {
	t.Helper()
	cfg := versionConfig(version)
	if !supportsLinemateContext(version) {
		cfg.LinemateRegressionStrength = 0
	}
	input := pinnedInput()
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	evaluations, err := Evaluate(cfg, input)
	require.NoError(t, err)
	return pinnedHashes{
		config:     configHash(snapshot.Config),
		source:     snapshot.SourceDataHash,
		evaluation: evaluationDataHash(snapshot.Config, input),
		players:    hashValue(snapshot.Players),
		evaluated:  hashValue(evaluations),
	}
}

// pinnedInput has a starter who sat on the bench in a third of the games he
// dressed for, a backup who dressed far more often than he played, and one
// skater so both evaluation kinds have outcomes.
func pinnedInput() Input {
	asOf := time.Date(2025, time.September, 20, 0, 0, 0, 0, time.UTC)
	starterBirth := time.Date(1998, time.March, 4, 0, 0, 0, 0, time.UTC)
	backupBirth := time.Date(2001, time.June, 18, 0, 0, 0, 0, time.UTC)
	return Input{
		TargetSeason: pinTargetSeason, AsOf: asOf, ObservedAt: asOf.AddDate(1, 0, 0),
		SourceMaxGameDate: time.Date(2025, time.April, 17, 0, 0, 0, 0, time.UTC),
		Goalies: []GoalieSeason{
			pinGoalie(pinStarterID, starterBirth, 20222023, 70, 50, 48),
			pinGoalie(pinStarterID, starterBirth, 20232024, 80, 60, 58),
			pinGoalie(pinStarterID, starterBirth, 20242025, 82, 62, 60),
			pinGoalie(pinStarterID, starterBirth, pinTargetSeason, 77, 43, 42),
			pinGoalie(pinBackupID, backupBirth, 20232024, 30, 5, 4),
			pinGoalie(pinBackupID, backupBirth, 20242025, 48, 16, 15),
			pinGoalie(pinBackupID, backupBirth, pinTargetSeason, 53, 25, 23),
		},
		Skaters: []SkaterSeason{
			pinSkater(20242025, 80, 70_000, 30),
			pinSkater(pinTargetSeason, 78, 68_000, 27),
		},
	}
}

// pinGoalie builds a season in which the goalie dressed for dressed games,
// played appeared of them and started started. Per-appearance workload is
// fixed so totals scale with appearances, as in real boxscores where a bench
// game adds a row with zero time on ice and zero shots.
func pinGoalie(id int64, birth time.Time, season, dressed, appeared, started int) GoalieSeason {
	const (
		toiPerAppearance   = 3_300
		shotsPerAppearance = 28
		savesPerAppearance = 25
	)
	return GoalieSeason{
		PlayerID: id, BirthDate: birth, TeamID: 8, Season: season,
		GamesPlayed: dressed, GamesAppeared: appeared, GamesStarted: started,
		TOISeconds: appeared * toiPerAppearance, Wins: started / 2, Shutouts: started / 12,
		ShotsAgainst: appeared * shotsPerAppearance, Saves: appeared * savesPerAppearance,
		GoalsAgainst: appeared * (shotsPerAppearance - savesPerAppearance),
	}
}

func pinSkater(season, games, toi, points int) SkaterSeason {
	return SkaterSeason{
		PlayerID: pinSkaterID, BirthDate: time.Date(1999, time.May, 1, 0, 0, 0, 0, time.UTC),
		TeamID: 8, Season: season, Position: "C", GamesPlayed: games, TOISeconds: toi,
		Goals: points / 2, Assists: points - points/2, ShotsOnGoal: 3 * points, Hits: games,
		BlockedShots: games / 2, FaceoffsWon: 400, FaceoffsLost: 380,
	}
}
