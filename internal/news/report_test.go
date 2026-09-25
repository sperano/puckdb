package news

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var reportGeneratedAt = time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)

func writeDigestString(t *testing.T, d Digest) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, WriteDigest(&b, d))
	return b.String()
}

func TestWriteDigestCoverageTableAndWarning(t *testing.T) {
	out := writeDigestString(t, Digest{
		GeneratedAt: reportGeneratedAt, Season: testSeason,
		Coverage: []SourceCoverage{
			{Source: Source{ID: "nhl-injury", Publisher: "NHL.com", Kind: KindOfficial, Priority: 20}, Status: CoverageFresh},
			{Source: Source{ID: "sportsnet-nhl", Publisher: "Sportsnet", Kind: KindReporting, Priority: 70}, Status: CoverageStale},
		},
	})
	assert.Contains(t, out, "| Source | Publisher | Kind | Priority | Status | Data as of | Last attempt | Failures | Last error |")
	assert.Contains(t, out, "> **Warning:** sportsnet-nhl is stale: its news may be missing or out of date.")
	assert.NotContains(t, out, "nhl-injury is fresh", "only non-current sources get a warning")
}

func incidentEvidence() []Evidence {
	return []Evidence{
		{Relation: RelationIndependent, Publisher: "NHL.com", Kind: KindOfficial, Title: "McNabb suspended",
			URL: "https://nhl.com/a", RetrievedAt: retrievedAt, Version: 1, LatestVersion: 2},
		{Relation: RelationIndependent, Publisher: "RotoWire", Kind: KindReporting, Title: "McNabb banned",
			URL: "https://rotowire.com/a", PublishedAt: retrievedAt, RetrievedAt: retrievedAt, Version: 1, LatestVersion: 1},
		{Relation: RelationSyndicated, Publisher: "Team Site", Kind: KindOfficial, Title: "McNabb suspended",
			URL: "https://vegas.nhl.com/a", PublishedAt: retrievedAt, RetrievedAt: retrievedAt, Version: 1, LatestVersion: 1},
	}
}

func TestWriteDigestIncidentHeadingAndEvidence(t *testing.T) {
	inc := Incident{
		PlayerName: "Brayden McNabb", NHLPlayerID: mcnabbID, Category: CategorySuspension,
		FirstReportedAt: retrievedAt, LastReportedAt: retrievedAt, Evidence: incidentEvidence(),
	}
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason, Incidents: []Incident{inc}})

	assert.Contains(t, out, "Brayden McNabb (NHL 8475188): suspension")
	assert.Contains(t, out, "Independent sources: NHL.com, RotoWire; repeated reports: 1")
	assert.Contains(t, out, "(official, independent)")
	assert.Contains(t, out, "(reporting, independent)")
	assert.Contains(t, out, "(official, syndicated)")
	assert.Contains(t, out, "published unknown", "the first evidence has no published time")
	assert.Contains(t, out, "version 1 of 2", "the first evidence is outdated")
}

func TestWriteDigestSupersededNote(t *testing.T) {
	inc := Incident{
		PlayerName: "Alex Lyon", NHLPlayerID: lyonID, Category: CategoryInjury,
		Evidence: []Evidence{
			{Relation: RelationIndependent, Publisher: "RotoWire", Version: 1, LatestVersion: 2},
			{Relation: RelationRevision, Publisher: "RotoWire", Version: 2, LatestVersion: 3},
		},
	}
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason, Incidents: []Incident{inc}})
	assert.Contains(t, out, "Every report behind this incident has a newer version that no longer supports it")
}

func TestWriteDigestNoIncidentsSaysNoneOnRecord(t *testing.T) {
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason})
	assert.Contains(t, out, "None on record. "+absenceCaveat)
}

func TestWriteDigestPlayerSection(t *testing.T) {
	player := PlayerNews{
		Player:   Identity{Name: "Alex Lyon", NHLPlayerID: lyonID},
		Coverage: []SourceCoverage{{Status: CoverageFresh}},
	}
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason, Player: &player})
	assert.Contains(t, out, "## Alex Lyon (NHL 8477494)")
	assert.Contains(t, out, player.Assessment())
}

func TestWriteDigestUnattachedSubjectsTable(t *testing.T) {
	issue := MentionIssue{
		Mention: "elias pettersson", Resolution: ResolutionAmbiguous, Method: MethodName,
		Candidates: []Identity{
			{Name: "Elias Pettersson", NHLPlayerID: petterssonCID, Team: "VAN"},
			{Name: "Elias Pettersson", NHLPlayerID: petterssonDID, Team: "VAN"},
		},
		Title: "Elias Pettersson update", URL: "https://rotowire.com/pettersson", Publisher: "RotoWire", RetrievedAt: retrievedAt,
	}
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason, Issues: []MentionIssue{issue}})
	assert.Contains(t, out, "| Name | Resolution | Reason | Candidates | Story | Publisher | Retrieved |")
	assert.Contains(t, out, "Elias Pettersson (NHL 8480012) VAN")
	assert.Contains(t, out, "Elias Pettersson (NHL 8483678) VAN")
}

func TestWriteDigestNoIssuesSaysNone(t *testing.T) {
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason})
	assert.Contains(t, out, "## Unattached story subjects\n\nNone.\n")
}

func TestWriteDigestEscapesPipeInTableCells(t *testing.T) {
	issue := MentionIssue{Mention: "A | B", Resolution: ResolutionUnresolved, Method: MethodTitlePrefix, Title: "T", URL: "https://x", Publisher: "P"}
	out := writeDigestString(t, Digest{GeneratedAt: reportGeneratedAt, Season: testSeason, Issues: []MentionIssue{issue}})
	assert.Contains(t, out, `A \| B`)
}
