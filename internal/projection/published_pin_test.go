package projection

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	pinTargetSeason = 20252026
	pinStarterID    = 8_001
	pinBackupID     = 8_002
	pinSkaterID     = 8_003
	pinClubID       = 8

	// pinRecentStarts and pinBackupRecentStarts shape the goalie start share
	// of goalieSharePinnedInput: the backup took the most recent ones, the
	// first pinPlayoffStarts of them in the playoffs.
	pinRecentStarts       = 40
	pinBackupRecentStarts = 25
	pinPlayoffStarts      = 6

	// pinOutputSignificantDigits is the precision the generated players and
	// evaluation metrics are pinned at. Go fuses multiply-adds on arm64 but
	// not on amd64, so the last bits of a float depend on the machine;
	// rounding keeps one pin valid on both while a model change still
	// moves it.
	pinOutputSignificantDigits = 9
	// pinOutputZeroTolerance is the magnitude below which an output number
	// is pinned as zero: an error that is zero in exact arithmetic comes out
	// as rounding noise (around 1e-16) whose digits differ by machine, which
	// relative rounding keeps.
	pinOutputZeroTolerance = 1e-9
)

// pinnedHashes are the identities a published model version computed for
// pinnedInput (or its pinInputs entry) before the next version was added,
// and the hashes of its outputs (mustHashOutputs). The identities must never
// change: stored snapshots and evaluations are looked up and verified by
// them.
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
		players:    "d87a909945f96add06bbb6304be5add58b28535144b133a98615d9def677acd3",
		evaluated:  "41b15477e4111dc97c6cd1b6d4cbe85b4fa9bf1e81bd0c88c349edf189f2a41b",
	},
	FaceoffModelVersion: {
		config:     "bdddb53fe417d350d27bc40bd6aba04188b752e5a5c74c1ae021bafab4567870",
		source:     "b43cb34295f4391dc406da26fc208adf7bcee7bbef821a267df95e05136fa14b",
		evaluation: "502304c9615559a8638152ce23c0baab46f06168d760b5d3a004144010b6ea6f",
		players:    "c1f69a605ce03b9c650db31f5e4b83d2d7c4b94eaa3014f36b2ec81b19c5d922",
		evaluated:  "1ee698d3f65858a0f44e0964731c513291638757f0d37c7613751cb55fe7a7de",
	},
	LinemateModelVersion: {
		config:     "6501fc75eacb50e375a1d4c63aa196e886c47d6b8cf990142674e9336d26a387",
		source:     "39720d261a624def4845916cdc49ff947052ec4236c6ce66326559403c707dc0",
		evaluation: "a1a875fa41ce2c235136208ca07f7dd35060fbacdf7b0bd21626b32334d2db49",
		players:    "c1f69a605ce03b9c650db31f5e4b83d2d7c4b94eaa3014f36b2ec81b19c5d922",
		evaluated:  "1349f6b55766fe66791b4cda88a5b18868a80214f5725004576c39bfe378953f",
	},
	AgingModelVersion: {
		config:     "be98d03df5b0e72cf6ca07e2c0308b11634014e6e9ad34ada9e2bfc2674ebbb6",
		source:     "6a4f16e9b206e4c2e8e651526a8f202208a3e11e5dc988a88f5cd305ce59084e",
		evaluation: "780b7d3d5135e9f0fd42a5234a2fe704218906cfa24029212ea8632a0b497e96",
		players:    "c1f69a605ce03b9c650db31f5e4b83d2d7c4b94eaa3014f36b2ec81b19c5d922",
		evaluated:  "fe472f8ba56c62e8b75884c1f5de5b193e1945053f4ef5d7d691b023cd81b057",
	},
	TeamEnvironmentModelVersion: {
		config:     "fbfa9a9840b9fdee3b4a6c65b82c17e9a5c3b2613d957b86ead706024ec2e56b",
		source:     "6a4f16e9b206e4c2e8e651526a8f202208a3e11e5dc988a88f5cd305ce59084e",
		evaluation: "780b7d3d5135e9f0fd42a5234a2fe704218906cfa24029212ea8632a0b497e96",
		players:    "c1f69a605ce03b9c650db31f5e4b83d2d7c4b94eaa3014f36b2ec81b19c5d922",
		evaluated:  "cd4dc585af3a8c9e504be3f66e2f2185dad7937423343468c283aa1083e54c6f",
	},
	// Captured before nhl-baseline-v7 added the goalie start share.
	GoalieAppearancesModelVersion: {
		config:     "a277dade264bc578d8e2fcb23835f5fd87e29cb8b7ce72541fd496c6aff005b2",
		source:     "72efb764d4666fe93c985b5def82181f97ee28993db8ae28c7eb141b6ec068a6",
		evaluation: "c5594953513c0717518e984d5c545420a4d6808556352c75f531c886af4ddcb0",
		players:    "d6f60db0b2e533cdcbf489b4c557c83aba3d64d61eabdd82e16e797d62d5915e",
		evaluated:  "9c1420eb1fa4147b30140afe6d911d1ed9a414624f5136888c2445e978b82ebf",
	},
	// Captured before any later version, on goalieSharePinnedInput (see
	// pinInputs) so the goalie start share is in every identity and output.
	GoalieShareModelVersion: {
		config:     "34a4bf433bb420180dd3943bb530c7a13de36b2a3ff8b917c72df29c01211bec",
		source:     "e06a1d5b84522520973284e8ad199653c23c6350d363e3cb5430f9a2223c0fc9",
		evaluation: "0b02098b30487c8efdbdbbdac786fb5e35af26b448e74e198f950b34f7f1db25",
		players:    "82cb05a4583482e52a02a9d1440d16c2ce71fc687ca488d9a3bad26e33cabc13",
		evaluated:  "bc7712df05257604ac26911f766d3c9c3f3c830bc2f9b3f2ef93b79e988bce52",
	},
}

// pinInputs gives the versions pinned on another input than pinnedInput.
var pinInputs = map[string]func() Input{
	GoalieShareModelVersion: goalieSharePinnedInput,
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
	if pinned, ok := pinInputs[version]; ok {
		input = pinned()
	}
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	evaluations, err := Evaluate(cfg, input)
	require.NoError(t, err)
	return pinnedHashes{
		config:     mustConfigHash(t, snapshot.Config),
		source:     snapshot.SourceDataHash,
		evaluation: mustEvaluationDataHash(t, snapshot.Config, input),
		players:    mustHashOutputs(t, snapshot.Players),
		evaluated:  mustHashOutputs(t, evaluations),
	}
}

// TestEveryModelVersionIsPinned keeps a new version from shipping without
// its pins, which the next version's change could otherwise move unseen.
func TestEveryModelVersionIsPinned(t *testing.T) {
	t.Parallel()

	require.Len(t, publishedPins, len(modelVersions))
	for _, v := range modelVersions {
		require.Contains(t, publishedPins, v.version)
	}
}

// mustHashOutputs hashes a generated value with every non-integer number
// rounded to pinOutputSignificantDigits, or to zero below
// pinOutputZeroTolerance.
func mustHashOutputs(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var tree any
	require.NoError(t, decoder.Decode(&tree))
	return mustHashJSON(t, roundFloats(t, tree))
}

func roundFloats(t *testing.T, node any) any {
	t.Helper()
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			value[key] = roundFloats(t, child)
		}
	case []any:
		for i, child := range value {
			value[i] = roundFloats(t, child)
		}
	case json.Number:
		if !strings.ContainsAny(value.String(), ".eE") {
			return value
		}
		f, err := value.Float64()
		require.NoError(t, err)
		if math.Abs(f) < pinOutputZeroTolerance {
			return json.Number("0")
		}
		return json.Number(strconv.FormatFloat(f, 'g', pinOutputSignificantDigits, 64))
	}
	return node
}

// TestRoundFloatsFlushesNoiseOnly checks the zero threshold and its sign
// directly, since the golden pins catch a wrong one only when a pinned
// output happens to lie between the wrong and the right value.
func TestRoundFloatsFlushesNoiseOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, in, want string
	}{
		{name: "positive noise", in: "5e-16", want: "0"},
		{name: "negative noise", in: "-5e-16", want: "0"},
		{name: "just below tolerance", in: "-9.99e-10", want: "0"},
		{name: "tolerance kept", in: "1e-9", want: "1e-09"},
		{name: "negative tolerance kept", in: "-1e-9", want: "-1e-09"},
		{name: "integer untouched", in: "42", want: "42"},
		{name: "normal float rounded", in: "1.23456789012", want: "1.23456789"},
		{name: "negative float rounded", in: "-2.50000000049", want: "-2.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, json.Number(tc.want), roundFloats(t, json.Number(tc.in)))
		})
	}
}

func TestRoundFloatsWalksNestedValues(t *testing.T) {
	t.Parallel()

	tree := map[string]any{
		"mae":  []any{json.Number("5e-16"), json.Number("0.1234567891")},
		"name": "skater",
	}
	require.Equal(t, map[string]any{
		"mae":  []any{json.Number("0"), json.Number("0.123456789")},
		"name": "skater",
	}, roundFloats(t, tree))
}

// TestGoalieSharePinReadsRecentStarts checks goalieSharePinnedInput
// exercises the start share, so the nhl-baseline-v7 pin covers it: without
// the recent starts the output differs, and the backup's late takeover
// raises his projected starts.
func TestGoalieSharePinReadsRecentStarts(t *testing.T) {
	t.Parallel()

	cfg := versionConfig(GoalieShareModelVersion)
	with := mustGenerate(t, cfg, goalieSharePinnedInput())
	withoutInput := goalieSharePinnedInput()
	withoutInput.GoalieStarts = nil
	without := mustGenerate(t, cfg, withoutInput)
	require.NotEqual(t, mustHashJSON(t, without.Players), mustHashJSON(t, with.Players))
	require.Greater(t, goalieStarts(t, with, pinBackupID), goalieStarts(t, without, pinBackupID))
	require.Less(t, goalieStarts(t, with, pinStarterID), goalieStarts(t, without, pinStarterID))
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

// goalieSharePinnedInput is pinnedInput with both goalies rostered by
// their club for the target season and the club's recent starts, which the
// backup took over late in the season and in the playoffs.
func goalieSharePinnedInput() Input {
	input := pinnedInput()
	input.TargetTeams = []PlayerTeam{
		{PlayerID: pinStarterID, TeamID: pinClubID}, {PlayerID: pinBackupID, TeamID: pinClubID},
	}
	lastDay := time.Date(2025, time.May, 20, 0, 0, 0, 0, time.UTC)
	for rank := 1; rank <= pinRecentStarts; rank++ {
		goalie := int64(pinStarterID)
		if rank <= pinBackupRecentStarts {
			goalie = pinBackupID
		}
		playoff := rank <= pinPlayoffStarts
		regularRank := 0
		if !playoff {
			regularRank = rank - pinPlayoffStarts
		}
		input.GoalieStarts = append(input.GoalieStarts, GoalieStart{
			TeamID: pinClubID, PlayerID: goalie, Season: 20242025,
			GameDate: lastDay.AddDate(0, 0, -2*rank), Playoff: playoff,
			RecencyRank: rank, RegularSeasonRank: regularRank,
		})
	}
	return input
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
		PlayerID: id, BirthDate: birth, TeamID: pinClubID, Season: season,
		GamesPlayed: dressed, GamesAppeared: appeared, GamesStarted: started,
		TOISeconds: appeared * toiPerAppearance, Wins: started / 2, Shutouts: started / 12,
		ShotsAgainst: appeared * shotsPerAppearance, Saves: appeared * savesPerAppearance,
		GoalsAgainst: appeared * (shotsPerAppearance - savesPerAppearance),
	}
}

func pinSkater(season, games, toi, points int) SkaterSeason {
	return SkaterSeason{
		PlayerID: pinSkaterID, BirthDate: time.Date(1999, time.May, 1, 0, 0, 0, 0, time.UTC),
		TeamID: pinClubID, Season: season, Position: "C", GamesPlayed: games, TOISeconds: toi,
		Goals: points / 2, Assists: points - points/2, ShotsOnGoal: 3 * points, Hits: games,
		BlockedShots: games / 2, FaceoffsWon: 400, FaceoffsLost: 380,
	}
}
