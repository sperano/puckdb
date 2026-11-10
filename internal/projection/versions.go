package projection

// capability is a model feature gated by version. The version that
// introduces a capability and every later version support it; the gates
// below are the only way model code asks.
type capability int

const (
	capFaceoffs capability = iota
	capLinemateContext
	capAgingCurve
	capTeamEnvironment
	capGoalieAppearances
	capGoalieStartShare
)

// modelVersions lists every model version oldest first with the
// capabilities it introduces. A stored snapshot keeps the version that
// produced it, so a row is never edited once published: a new version is
// appended, and CurrentModelVersion moved to it, without changing what an
// earlier version supports.
var modelVersions = []struct {
	version    string
	introduces []capability
}{
	{version: LegacyModelVersion},
	{version: FaceoffModelVersion, introduces: []capability{capFaceoffs}},
	{version: LinemateModelVersion, introduces: []capability{capLinemateContext}},
	{version: AgingModelVersion, introduces: []capability{capAgingCurve}},
	{version: TeamEnvironmentModelVersion, introduces: []capability{capTeamEnvironment}},
	{version: GoalieAppearancesModelVersion, introduces: []capability{capGoalieAppearances}},
	{version: GoalieShareModelVersion, introduces: []capability{capGoalieStartShare}},
}

// knownModelVersion reports whether modelVersion is in modelVersions.
func knownModelVersion(modelVersion string) bool {
	for _, v := range modelVersions {
		if v.version == modelVersion {
			return true
		}
	}
	return false
}

// supports reports whether modelVersion or an earlier version introduced c.
// An unknown version supports nothing; Validate refuses it.
func supports(modelVersion string, c capability) bool {
	introduced := false
	for _, v := range modelVersions {
		for _, got := range v.introduces {
			introduced = introduced || got == c
		}
		if v.version == modelVersion {
			return introduced
		}
	}
	return false
}

func supportsFaceoffs(modelVersion string) bool {
	return supports(modelVersion, capFaceoffs)
}

func supportsLinemateContext(modelVersion string) bool {
	return supports(modelVersion, capLinemateContext)
}

func supportsAgingCurve(modelVersion string) bool {
	return supports(modelVersion, capAgingCurve)
}

func supportsTeamEnvironment(modelVersion string) bool {
	return supports(modelVersion, capTeamEnvironment)
}

// countsGoalieAppearances reports whether a model counts a goalie's games as
// his appearances (GoalieSeason.GamesAppeared) instead of every game he
// dressed for (GamesPlayed, which includes games sat as the backup).
func countsGoalieAppearances(modelVersion string) bool {
	return supports(modelVersion, capGoalieAppearances)
}

// supportsGoalieStartShare reports whether a model blends a goalie's share
// of his club's recent starts into his projected starts.
func supportsGoalieStartShare(modelVersion string) bool {
	return supports(modelVersion, capGoalieStartShare)
}
