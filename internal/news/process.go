package news

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ProcessQueries is the database access ProcessVersion needs. Callers run it
// in one transaction per version so a retry never half-applies a version.
type ProcessQueries interface {
	DeleteNewsMentions(ctx context.Context, versionID int64) error
	InsertNewsMention(ctx context.Context, arg sqlcdb.InsertNewsMentionParams) error
	ListNewsIncidentCandidates(ctx context.Context, arg sqlcdb.ListNewsIncidentCandidatesParams) ([]sqlcdb.NewsIncident, error)
	ListNewsIncidentEvidenceKeys(ctx context.Context, incidentIDs []int64) ([]sqlcdb.ListNewsIncidentEvidenceKeysRow, error)
	CreateNewsIncident(ctx context.Context, arg sqlcdb.CreateNewsIncidentParams) (int64, error)
	ExtendNewsIncident(ctx context.Context, arg sqlcdb.ExtendNewsIncidentParams) error
	InsertNewsIncidentEvidence(ctx context.Context, arg sqlcdb.InsertNewsIncidentEvidenceParams) error
	MarkNewsVersionProcessed(ctx context.Context, arg sqlcdb.MarkNewsVersionProcessedParams) error
}

// ProcessOutcome counts what processing one or more versions did.
type ProcessOutcome struct {
	Versions         int `json:"versions"`
	Resolved         int `json:"resolved"`
	Ambiguous        int `json:"ambiguous"`
	Unresolved       int `json:"unresolved"`
	IncidentsCreated int `json:"incidentsCreated"`
	EvidenceAdded    int `json:"evidenceAdded"`
	// Repeats counts evidence that repeats a source already attached
	// (syndicated copies, same-publisher follow-ups, revisions).
	Repeats int `json:"repeats"`
}

// Add accumulates another outcome.
func (o *ProcessOutcome) Add(other ProcessOutcome) {
	o.Versions += other.Versions
	o.Resolved += other.Resolved
	o.Ambiguous += other.Ambiguous
	o.Unresolved += other.Unresolved
	o.IncidentsCreated += other.IncidentsCreated
	o.EvidenceAdded += other.EvidenceAdded
	o.Repeats += other.Repeats
}

// ProcessVersion resolves the players a version names, records the
// mentions, and attaches the version to an incident candidate for each
// resolved subject when the story has a category. Reprocessing a version
// replaces its mentions and adds no duplicate evidence.
func ProcessVersion(ctx context.Context, q ProcessQueries, dir *Directory, v sqlcdb.ListUnprocessedNewsVersionsRow,
	window time.Duration, now time.Time) (ProcessOutcome, error) {
	outcome := ProcessOutcome{Versions: 1}
	var subjects []Subject
	if err := json.Unmarshal(v.Subjects, &subjects); err != nil {
		return outcome, fmt.Errorf("decode subjects of version %d: %w", v.ID, err)
	}
	mentions := dir.Resolve(Story{
		Title: v.Title, Text: v.EvidenceText, Subjects: subjects, TeamHints: v.TeamHints,
		Structured: Kind(v.Kind) == KindStructured,
	})
	if err := saveMentions(ctx, q, v.ID, mentions); err != nil {
		return outcome, err
	}
	category := Category(v.CategoryHint)
	if category == CategoryNone {
		category = Classify(v.Title, v.EvidenceText)
	}
	report := Report{
		VersionID: v.ID, ArticleID: v.ArticleID, Publisher: v.Publisher, Kind: Kind(v.Kind),
		ReportedAt:       ReportedAt(timeOf(v.PublishedAt), timeOf(v.SourceUpdatedAt), timeOf(v.RetrievedAt)),
		TitleFingerprint: v.TitleFingerprint, TextFingerprint: v.TextFingerprint, Category: category,
	}
	attached := make(map[string]bool)
	for _, m := range mentions {
		outcome.count(m)
		if category == CategoryNone || m.Role != RoleSubject || m.Resolution != ResolutionResolved || attached[m.Player.Key()] {
			continue
		}
		attached[m.Player.Key()] = true
		if err := attach(ctx, q, report, m.Player, window, &outcome); err != nil {
			return outcome, err
		}
	}
	err := q.MarkNewsVersionProcessed(ctx, sqlcdb.MarkNewsVersionProcessedParams{ID: v.ID, ProcessedAt: Timestamptz(now)})
	return outcome, err
}

func (o *ProcessOutcome) count(m Mention) {
	switch m.Resolution {
	case ResolutionResolved:
		o.Resolved++
	case ResolutionAmbiguous:
		o.Ambiguous++
	case ResolutionUnresolved:
		o.Unresolved++
	}
}

func saveMentions(ctx context.Context, q ProcessQueries, versionID int64, mentions []Mention) error {
	if err := q.DeleteNewsMentions(ctx, versionID); err != nil {
		return fmt.Errorf("clear mentions of version %d: %w", versionID, err)
	}
	for i, m := range mentions {
		if m.Candidates == nil {
			m.Candidates = []Identity{}
		}
		candidates, err := json.Marshal(m.Candidates)
		if err != nil {
			return err
		}
		params := sqlcdb.InsertNewsMentionParams{
			VersionID: versionID, Ordinal: int32(i), Mention: m.Text, Role: string(m.Role),
			Resolution: string(m.Resolution), Method: string(m.Method), Candidates: candidates,
		}
		if m.Resolution == ResolutionResolved {
			params.NhlPlayerID, params.YahooPlayerID = playerIDs(m.Player)
		}
		if err := q.InsertNewsMention(ctx, params); err != nil {
			return fmt.Errorf("save mention %q of version %d: %w", m.Text, versionID, err)
		}
	}
	return nil
}

// attach adds the report to the player's matching incident, or starts one.
func attach(ctx context.Context, q ProcessQueries, r Report, player Identity, window time.Duration, outcome *ProcessOutcome) error {
	nhlID, yahooID := playerIDs(player)
	spans, evidence, err := nearbyIncidents(ctx, q, r, player, window)
	if err != nil {
		return err
	}
	incidentID, found := ChooseIncident(r, spans, evidence, window)
	relation, related := RelationIndependent, int64(0)
	if found {
		relation, related = Relate(r, evidenceOf(incidentID, evidence))
	} else {
		incidentID, err = q.CreateNewsIncident(ctx, sqlcdb.CreateNewsIncidentParams{
			NhlPlayerID: nhlID, YahooPlayerID: yahooID, PlayerName: player.Name,
			Category: string(r.Category), FirstReportedAt: Timestamptz(r.ReportedAt),
		})
		if err != nil {
			return fmt.Errorf("create %s incident for %s: %w", r.Category, player.Key(), err)
		}
		outcome.IncidentsCreated++
	}
	if err := q.ExtendNewsIncident(ctx, sqlcdb.ExtendNewsIncidentParams{
		ID: incidentID, ReportedAt: Timestamptz(r.ReportedAt), NhlPlayerID: nhlID, YahooPlayerID: yahooID,
	}); err != nil {
		return fmt.Errorf("extend incident %d: %w", incidentID, err)
	}
	relatedID := pgtype.Int8{Int64: related, Valid: related != 0}
	if err := q.InsertNewsIncidentEvidence(ctx, sqlcdb.InsertNewsIncidentEvidenceParams{
		IncidentID: incidentID, VersionID: r.VersionID, ArticleID: r.ArticleID, Publisher: r.Publisher,
		Kind: string(r.Kind), ReportedAt: Timestamptz(r.ReportedAt), Relation: string(relation), RelatedVersionID: relatedID,
	}); err != nil {
		return fmt.Errorf("attach version %d to incident %d: %w", r.VersionID, incidentID, err)
	}
	outcome.EvidenceAdded++
	if relation != RelationIndependent {
		outcome.Repeats++
	}
	return nil
}

// nearbyIncidents loads the player's incidents within window of the report
// and their evidence.
func nearbyIncidents(ctx context.Context, q ProcessQueries, r Report, player Identity, window time.Duration) ([]IncidentSpan, []EvidenceKey, error) {
	nhlID, yahooID := playerIDs(player)
	rows, err := q.ListNewsIncidentCandidates(ctx, sqlcdb.ListNewsIncidentCandidatesParams{
		NhlPlayerID: nhlID, YahooPlayerID: yahooID,
		Earliest: Timestamptz(r.ReportedAt.Add(-window)), Latest: Timestamptz(r.ReportedAt.Add(window)),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list incidents of %s: %w", player.Key(), err)
	}
	spans := make([]IncidentSpan, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		spans = append(spans, IncidentSpan{
			ID: row.ID, Category: Category(row.Category), First: timeOf(row.FirstReportedAt), Last: timeOf(row.LastReportedAt),
		})
		ids = append(ids, row.ID)
	}
	evidence, err := evidenceKeys(ctx, q, ids)
	return spans, evidence, err
}

func evidenceKeys(ctx context.Context, q ProcessQueries, incidentIDs []int64) ([]EvidenceKey, error) {
	if len(incidentIDs) == 0 {
		return nil, nil
	}
	rows, err := q.ListNewsIncidentEvidenceKeys(ctx, incidentIDs)
	if err != nil {
		return nil, fmt.Errorf("list incident evidence: %w", err)
	}
	keys := make([]EvidenceKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, EvidenceKey{
			IncidentID: row.IncidentID, VersionID: row.VersionID, ArticleID: row.ArticleID, Publisher: row.Publisher,
			TitleFingerprint: row.TitleFingerprint, TextFingerprint: row.TextFingerprint,
		})
	}
	return keys, nil
}

func evidenceOf(incidentID int64, evidence []EvidenceKey) []EvidenceKey {
	var out []EvidenceKey
	for _, e := range evidence {
		if e.IncidentID == incidentID {
			out = append(out, e)
		}
	}
	return out
}

func playerIDs(p Identity) (pgtype.Int8, pgtype.Int4) {
	return pgtype.Int8{Int64: p.NHLPlayerID, Valid: p.NHLPlayerID != 0},
		pgtype.Int4{Int32: int32(p.YahooPlayerID), Valid: p.YahooPlayerID != 0}
}
