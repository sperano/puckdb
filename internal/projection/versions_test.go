package projection

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// gates are the capability predicates model code calls, by capability.
var gates = map[capability]func(string) bool{
	capFaceoffs:          supportsFaceoffs,
	capLinemateContext:   supportsLinemateContext,
	capAgingCurve:        supportsAgingCurve,
	capTeamEnvironment:   supportsTeamEnvironment,
	capGoalieAppearances: countsGoalieAppearances,
	capGoalieStartShare:  supportsGoalieStartShare,
}

// TestModelVersionCapabilities spells out, for every published version,
// the capabilities it supports. A row here changes only when a version is
// added: bumping CurrentModelVersion must leave every earlier row as is.
func TestModelVersionCapabilities(t *testing.T) {
	t.Parallel()

	want := []struct {
		version  string
		supports []capability
	}{
		{"nhl-baseline-v1", nil},
		{"nhl-baseline-v2", []capability{capFaceoffs}},
		{"nhl-baseline-v3", []capability{capFaceoffs, capLinemateContext}},
		{"nhl-baseline-v4", []capability{capFaceoffs, capLinemateContext, capAgingCurve}},
		{"nhl-baseline-v5", []capability{capFaceoffs, capLinemateContext, capAgingCurve, capTeamEnvironment}},
		{"nhl-baseline-v6", []capability{
			capFaceoffs, capLinemateContext, capAgingCurve, capTeamEnvironment, capGoalieAppearances,
		}},
		{"nhl-baseline-v7", []capability{
			capFaceoffs, capLinemateContext, capAgingCurve, capTeamEnvironment, capGoalieAppearances,
			capGoalieStartShare,
		}},
	}
	require.Len(t, modelVersions, len(want), "a new model version needs a row here")
	for i, row := range want {
		require.Equal(t, row.version, modelVersions[i].version, "versions are listed oldest first")
		require.True(t, knownModelVersion(row.version), row.version)
		for c, gate := range gates {
			supported := false
			for _, s := range row.supports {
				supported = supported || s == c
			}
			require.Equal(t, supported, gate(row.version), "%s capability %d", row.version, c)
		}
	}
	require.Equal(t, "nhl-baseline-v7", CurrentModelVersion)
	require.Equal(t, CurrentModelVersion, modelVersions[len(modelVersions)-1].version)
}

// TestEveryCapabilityIsIntroducedOnce checks the table itself: each version
// appears once and each capability is introduced by exactly one version, so
// supports never depends on which duplicate it meets first.
func TestEveryCapabilityIsIntroducedOnce(t *testing.T) {
	t.Parallel()

	versions := map[string]bool{}
	introduced := map[capability]int{}
	for _, v := range modelVersions {
		require.False(t, versions[v.version], "duplicate version %s", v.version)
		versions[v.version] = true
		for _, c := range v.introduces {
			introduced[c]++
		}
	}
	require.Len(t, introduced, len(gates))
	for c := range gates {
		require.Equal(t, 1, introduced[c], "capability %d", c)
	}
}

func TestUnknownModelVersionSupportsNothingAndIsRefused(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"nhl-baseline-v8", "nhl-baseline-v0", "NHL-BASELINE-V7", "nhl-baseline-v7 "} {
		for c, gate := range gates {
			require.False(t, gate(version), "%q capability %d", version, c)
		}
		require.False(t, knownModelVersion(version), version)
		cfg := versionConfig(version)
		cfg.LinemateRegressionStrength = 0
		require.EqualError(t, cfg.Validate(), `unknown model version "`+version+`"`)
	}
}

// TestDefaultConfigUsesCurrentModelVersion checks the default config is
// valid for the current version, which supports every capability, and that
// the settings a capability needs are not left at zero, which would turn it
// off. Faceoffs and goalie appearances have no setting; the aging curve is
// fitted from the input when the config has none (Generate).
func TestDefaultConfigUsesCurrentModelVersion(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	require.Equal(t, CurrentModelVersion, cfg.ModelVersion)
	require.NoError(t, cfg.Validate())
	for c, gate := range gates {
		require.True(t, gate(cfg.ModelVersion), "capability %d", c)
	}
	require.Positive(t, cfg.LinemateRegressionStrength, "linemate context")
	require.Nil(t, cfg.AgingCurve, "aging curve is fitted per input")
	require.Positive(t, cfg.TeamEnvironmentPriorGames, "team environment")
	require.Positive(t, cfg.TeamEnvironmentMaxChange, "team environment")
	require.Positive(t, cfg.GoalieShareWindowGames, "goalie start share")
	require.Positive(t, cfg.GoalieShareHalfLifeGames, "goalie start share")
	require.Positive(t, cfg.GoalieShareBlend, "goalie start share")
}
