package news

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var clusterBase = time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

func TestRelate(t *testing.T) {
	evidence := []EvidenceKey{
		{IncidentID: 1, VersionID: 10, ArticleID: 100, Publisher: "NHL.com", TitleFingerprint: "t1", TextFingerprint: "x1"},
	}
	tests := []struct {
		name     string
		report   Report
		relation Relation
		related  int64
	}{
		{"new version of the same article", Report{ArticleID: 100, Publisher: "NHL.com"}, RelationRevision, 10},
		{"copy with the same title", Report{ArticleID: 101, Publisher: "Team site", TitleFingerprint: "t1"}, RelationSyndicated, 10},
		{"copy with the same text", Report{ArticleID: 102, Publisher: "Wire", TextFingerprint: "x1"}, RelationSyndicated, 10},
		{"follow-up from the same publisher", Report{ArticleID: 103, Publisher: "NHL.com", TitleFingerprint: "t2"}, RelationSamePublisher, 10},
		{"another outlet's own report", Report{ArticleID: 104, Publisher: "RotoWire", TitleFingerprint: "t3"}, RelationIndependent, 0},
		{"empty fingerprints never match", Report{ArticleID: 105, Publisher: "ESPN"}, RelationIndependent, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relation, related := Relate(tt.report, evidence)
			assert.Equal(t, tt.relation, relation)
			assert.Equal(t, tt.related, related)
		})
	}
}

func TestChooseIncident(t *testing.T) {
	window := 7 * 24 * time.Hour
	injury := IncidentSpan{ID: 1, Category: CategoryInjury, First: clusterBase, Last: clusterBase.Add(48 * time.Hour)}
	day := func(n int) time.Time { return clusterBase.Add(time.Duration(n) * 24 * time.Hour) }

	id, ok := ChooseIncident(Report{Category: CategoryInjury, ReportedAt: day(5)}, []IncidentSpan{injury}, nil, window)
	assert.True(t, ok)
	assert.Equal(t, int64(1), id, "within the window of the incident's last report")

	_, ok = ChooseIncident(Report{Category: CategoryInjury, ReportedAt: day(10)}, []IncidentSpan{injury}, nil, window)
	assert.False(t, ok, "beyond the window a report starts a new incident")

	_, ok = ChooseIncident(Report{Category: CategorySuspension, ReportedAt: day(1)}, []IncidentSpan{injury}, nil, window)
	assert.False(t, ok, "another category is another incident")

	reinstated := IncidentSpan{ID: 2, Category: CategoryReinstatement, First: day(3), Last: day(3)}
	_, ok = ChooseIncident(Report{Category: CategoryInjury, ReportedAt: day(4)}, []IncidentSpan{injury, reinstated}, nil, window)
	assert.False(t, ok, "an injury reported after a reinstatement is a new injury")

	id, ok = ChooseIncident(Report{Category: CategoryInjury, ReportedAt: day(1)}, []IncidentSpan{injury, reinstated}, nil, window)
	assert.True(t, ok, "a late-retrieved report from before the reinstatement joins the old injury")
	assert.Equal(t, int64(1), id)

	older := IncidentSpan{ID: 3, Category: CategoryInjury, First: day(-6), Last: day(-6)}
	evidence := []EvidenceKey{{IncidentID: 3, ArticleID: 50}}
	id, ok = ChooseIncident(Report{Category: CategoryInjury, ArticleID: 50, ReportedAt: day(1)}, []IncidentSpan{injury, older}, evidence, window)
	assert.True(t, ok)
	assert.Equal(t, int64(3), id, "a revision joins the incident its earlier version is on")
}
