package newsevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ExtractionStatus is where one extraction stands.
type ExtractionStatus string

const (
	// ExtractionPending: attempted, no outcome recorded (the attempt is
	// still running, or crashed).
	ExtractionPending ExtractionStatus = "pending"
	// ExtractionSucceeded: the reply followed the schema; its validated
	// events are reconciled.
	ExtractionSucceeded ExtractionStatus = "succeeded"
	// ExtractionInvalid: the reply did not follow the schema. Not retried
	// under the same extractor; listed for review.
	ExtractionInvalid ExtractionStatus = "invalid"
	// ExtractionFailed: the model call failed. Retried after a delay until
	// the attempts run out, then listed for review.
	ExtractionFailed ExtractionStatus = "failed"
	// ExtractionDeferred: not attempted now, because the run's budget is
	// spent or another refresh is extracting the version.
	ExtractionDeferred ExtractionStatus = "deferred"
)

const (
	// firstDocumentRef and playerRefPrefix name the inputs the model cites.
	firstDocumentRef = "E1"
	playerRefPrefix  = "P"
	// maxRecordedErrorRunes bounds the error text kept on an extraction.
	maxRecordedErrorRunes = 500
	// emptyReply is recorded for a version that names no resolved player.
	emptyReply = `{"events": []}`
)

// RunQueries is the extraction bookkeeping a Runner needs outside the
// reconciling transaction.
type RunQueries interface {
	ListNewsVersionPlayers(ctx context.Context, versionID int64) ([]sqlcdb.ListNewsVersionPlayersRow, error)
	ListNewsArticleEventPlayers(ctx context.Context, arg sqlcdb.ListNewsArticleEventPlayersParams) ([]sqlcdb.ListNewsArticleEventPlayersRow, error)
	GetNewsExtraction(ctx context.Context, arg sqlcdb.GetNewsExtractionParams) (sqlcdb.NewsExtraction, error)
	StartNewsExtraction(ctx context.Context, arg sqlcdb.StartNewsExtractionParams) (sqlcdb.StartNewsExtractionRow, error)
	FindCachedNewsExtraction(ctx context.Context, arg sqlcdb.FindCachedNewsExtractionParams) (sqlcdb.FindCachedNewsExtractionRow, error)
	FinishNewsExtraction(ctx context.Context, arg sqlcdb.FinishNewsExtractionParams) error
	NoteNewsExtractionReconcileFailure(ctx context.Context, arg sqlcdb.NoteNewsExtractionReconcileFailureParams) error
}

// TxQueries is what the reconciling transaction needs.
type TxQueries interface {
	StoreQueries
	IsNewsExtractionReconciled(ctx context.Context, id int64) (bool, error)
	MarkNewsExtractionReconciled(ctx context.Context, arg sqlcdb.MarkNewsExtractionReconciledParams) error
}

// Budget decides whether another model call may be made, and is told what
// each call used. Release returns a reserved call that was not made.
type Budget interface {
	Reserve() bool
	Release()
	Spend(usage llm.Usage)
}

// Runner extracts and reconciles one article version at a time. It is safe
// for concurrent use when its queries and transaction function are.
type Runner struct {
	Extractor Extractor
	Queries   RunQueries
	// InTx runs fn in one transaction that holds the news event lock.
	InTx func(ctx context.Context, fn func(q TxQueries) error) error
	// Window is how far apart two reports of one event may be.
	Window time.Duration
	// MaxInputRunes bounds the article text given to the model.
	MaxInputRunes int
	// RetryAfter is the wait before a failed attempt is tried again; an
	// attempt another refresh started more recently is left to it.
	RetryAfter time.Duration
	Now        func() time.Time
}

// VersionResult is what extracting one version did.
type VersionResult struct {
	VersionID int64            `json:"versionId"`
	Status    ExtractionStatus `json:"status"`
	Called    bool             `json:"called"`
	Cached    bool             `json:"cached"`
	Usage     llm.Usage        `json:"usage"`
	// Claims, Unsupported and Issues come from validation.
	Claims      int     `json:"claims"`
	Unsupported int     `json:"unsupported"`
	Issues      int     `json:"issues"`
	Events      Outcome `json:"events"`
	Error       string  `json:"error,omitempty"`
}

// job is one version's extraction in progress.
type job struct {
	row          sqlcdb.ListNewsVersionsToExtractRow
	in           Input
	hash         string
	extractionID int64
	reply        string
	cachedFrom   int64
	result       VersionResult
}

// Run extracts one version: it reuses a stored output of the same extractor
// for an identical input, otherwise calls the model within budget, then
// validates the reply and reconciles its events. Only database failures
// are returned as errors; a failed or invalid reply is recorded on the
// extraction and in the result.
func (rn *Runner) Run(ctx context.Context, row sqlcdb.ListNewsVersionsToExtractRow, budget Budget) (VersionResult, error) {
	j := &job{row: row, result: VersionResult{VersionID: row.ID}}
	var err error
	if j.in, err = BuildInput(ctx, rn.Queries, row, rn.MaxInputRunes); err != nil {
		return j.result, err
	}
	j.hash = InputHash(j.in)
	prior, err := rn.Queries.GetNewsExtraction(ctx, sqlcdb.GetNewsExtractionParams{VersionID: row.ID, ExtractorKey: rn.Extractor.Key()})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return j.result, fmt.Errorf("load extraction of version %d: %w", row.ID, err)
	}
	if err == nil && ExtractionStatus(prior.Status) == ExtractionSucceeded && !prior.ReconciledAt.Valid {
		// A crash between recording the output and reconciling it.
		j.extractionID, j.reply = prior.ID, prior.RawOutput
		return rn.reconcile(ctx, j)
	}
	proceed, err := rn.obtainReply(ctx, j, budget)
	if err != nil || !proceed {
		return j.result, err
	}
	return rn.reconcile(ctx, j)
}

// obtainReply records an attempt and gets a reply from the cache or the
// model. It reports false when there is nothing to reconcile.
func (rn *Runner) obtainReply(ctx context.Context, j *job, budget Budget) (bool, error) {
	x := rn.Extractor
	cached, err := rn.Queries.FindCachedNewsExtraction(ctx, sqlcdb.FindCachedNewsExtractionParams{
		ExtractorKey: x.Key(), InputHash: j.hash,
	})
	hit := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("look up cached extraction: %w", err)
	}
	noPlayers := len(j.in.Players) == 0
	reserved := !hit && !noPlayers
	if reserved && !budget.Reserve() {
		j.result.Status = ExtractionDeferred
		return false, nil
	}
	now := rn.Now()
	started, err := rn.Queries.StartNewsExtraction(ctx, sqlcdb.StartNewsExtractionParams{
		VersionID: j.row.ID, ExtractorKey: x.Key(), Provider: x.Provider, Model: x.Model,
		PromptVersion: PromptVersion, SchemaVersion: SchemaVersion, InputHash: j.hash,
		LastAttemptAt: news.Timestamptz(now), StaleBefore: news.Timestamptz(now.Add(-rn.RetryAfter)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another refresh is extracting this version right now.
		if reserved {
			budget.Release()
		}
		j.result.Status = ExtractionDeferred
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record extraction attempt of version %d: %w", j.row.ID, err)
	}
	j.extractionID = started.ID
	if noPlayers {
		// Nothing the model could name: no call, no events.
		j.reply = emptyReply
		return true, nil
	}
	if hit {
		j.reply, j.cachedFrom, j.result.Cached = cached.RawOutput, cached.ID, true
		return true, nil
	}
	j.result.Called = true
	reply, err := x.Call(ctx, j.in)
	budget.Spend(reply.Usage)
	j.result.Usage = reply.Usage
	switch {
	case err != nil:
		return false, rn.finish(ctx, j, ExtractionFailed, err.Error(), Validation{})
	case reply.Rejected != nil:
		j.reply = reply.Content
		return false, rn.finish(ctx, j, ExtractionInvalid, reply.Rejected.Error(), Validation{})
	}
	j.reply = reply.Content
	return true, nil
}

// reconcile validates the reply and, when it follows the schema, applies
// its events in one transaction with marking the extraction reconciled. A
// failed reconciliation is recorded as an attempt and the batch goes on; it
// is an error only when even that cannot be recorded.
func (rn *Runner) reconcile(ctx context.Context, j *job) (VersionResult, error) {
	v, err := Interpret(j.reply, j.in)
	if err != nil {
		return j.result, rn.finish(ctx, j, ExtractionInvalid, err.Error(), Validation{})
	}
	if err := rn.finish(ctx, j, ExtractionSucceeded, "", v); err != nil {
		return j.result, err
	}
	r := reportOf(j.row, j.extractionID, v.Clean())
	err = rn.InTx(ctx, func(q TxQueries) error { return rn.applyEvents(ctx, q, j, r, v) })
	if err == nil {
		return j.result, nil
	}
	message := fmt.Sprintf("reconcile events: %v", err)
	noted := rn.Queries.NoteNewsExtractionReconcileFailure(ctx, sqlcdb.NoteNewsExtractionReconcileFailureParams{
		ID: j.extractionID, LastAttemptAt: news.Timestamptz(rn.Now()), LastError: news.CleanText(message, maxRecordedErrorRunes),
	})
	if noted != nil {
		return j.result, fmt.Errorf("reconcile events of version %d: %w", j.row.ID, errors.Join(err, noted))
	}
	j.result.Status, j.result.Error, j.result.Events = ExtractionFailed, message, Outcome{}
	return j.result, nil
}

// applyEvents reconciles the validated events under the event lock, unless
// another refresh reconciled this extraction since it was listed.
func (rn *Runner) applyEvents(ctx context.Context, q TxQueries, j *job, r Report, v Validation) error {
	done, err := q.IsNewsExtractionReconciled(ctx, j.extractionID)
	if err != nil || done {
		return err
	}
	existing, err := LoadNear(ctx, q, playersOf(j.in), r, rn.Window)
	if err != nil {
		return err
	}
	plan := Reconcile(existing, r, v.Events, rn.Window)
	if j.result.Events, err = Apply(ctx, q, plan, r, rn.Now()); err != nil {
		return err
	}
	return q.MarkNewsExtractionReconciled(ctx, sqlcdb.MarkNewsExtractionReconciledParams{
		ID: j.extractionID, ReconciledAt: news.Timestamptz(rn.Now()),
	})
}

// finish records an extraction's outcome.
func (rn *Runner) finish(ctx context.Context, j *job, status ExtractionStatus, message string, v Validation) error {
	j.result.Status, j.result.Error = status, message
	j.result.Claims, j.result.Unsupported, j.result.Issues = v.Claims, v.Unsupported, len(v.Issues)
	issues := v.Issues
	if issues == nil {
		issues = []Issue{}
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		return err
	}
	err = rn.Queries.FinishNewsExtraction(ctx, sqlcdb.FinishNewsExtractionParams{
		ID: j.extractionID, Status: string(status), LastError: news.CleanText(message, maxRecordedErrorRunes),
		RawOutput: j.reply, Issues: encoded, Events: int32(len(v.Events)),
		AddPromptTokens: int32(j.result.Usage.PromptTokens), AddCompletionTokens: int32(j.result.Usage.CompletionTokens),
		CachedFromID: pgInt8(j.cachedFrom),
	})
	if err != nil {
		return fmt.Errorf("record extraction of version %d: %w", j.row.ID, err)
	}
	return nil
}

// reportOf describes a version for reconciling.
func reportOf(row sqlcdb.ListNewsVersionsToExtractRow, extractionID int64, clean bool) Report {
	return Report{
		VersionID: row.ID, ArticleID: row.ArticleID, Version: int(row.Version), Publisher: row.Publisher,
		Kind: news.Kind(row.Kind), ExtractionID: extractionID, Clean: clean,
		ReportedAt: news.ReportedAt(row.PublishedAt.Time, row.SourceUpdatedAt.Time, row.RetrievedAt.Time).UTC(),
	}
}

func playersOf(in Input) []news.Identity {
	out := make([]news.Identity, len(in.Players))
	for i, p := range in.Players {
		out[i] = p.Identity
	}
	return out
}

// BuildInput gathers what the model sees for a version: the article, the
// players the resolver attached to it, and the players of events an
// earlier version of the article reported.
func BuildInput(ctx context.Context, q RunQueries, row sqlcdb.ListNewsVersionsToExtractRow, maxRunes int) (Input, error) {
	r := reportOf(row, 0, false)
	in := Input{Documents: []Document{{
		Ref: firstDocumentRef, VersionID: row.ID, ArticleID: row.ArticleID, Version: int(row.Version),
		Publisher: row.Publisher, Kind: news.Kind(row.Kind), Author: row.Author, Title: row.Title,
		Text: DocumentText(row.EvidenceText, row.Body, maxRunes), ReportedAt: r.ReportedAt,
	}}}
	mentions, err := q.ListNewsVersionPlayers(ctx, row.ID)
	if err != nil {
		return in, fmt.Errorf("list players of version %d: %w", row.ID, err)
	}
	var identities []news.Identity
	for _, m := range mentions {
		identities = append(identities, identity(m.NhlPlayerID.Int64, int(m.YahooPlayerID.Int32), m.Name))
	}
	earlier, err := q.ListNewsArticleEventPlayers(ctx, sqlcdb.ListNewsArticleEventPlayersParams{ArticleID: row.ArticleID, Version: row.Version})
	if err != nil {
		return in, fmt.Errorf("list event players of article %d: %w", row.ArticleID, err)
	}
	for _, p := range earlier {
		identities = append(identities, identity(p.NhlPlayerID.Int64, int(p.YahooPlayerID.Int32), p.PlayerName))
	}
	for _, id := range identities {
		if !containsPlayer(in.Players, id) {
			in.Players = append(in.Players, Player{Ref: playerRefPrefix + strconv.Itoa(len(in.Players)+1), Identity: id})
		}
	}
	return in, nil
}

func identity(nhlID int64, yahooID int, name string) news.Identity {
	if name == "" {
		name = news.Identity{NHLPlayerID: nhlID, YahooPlayerID: yahooID}.Key()
	}
	return news.Identity{NHLPlayerID: nhlID, YahooPlayerID: yahooID, Name: name}
}

func containsPlayer(players []Player, id news.Identity) bool {
	for _, p := range players {
		if samePlayer(p.Identity, id) {
			return true
		}
	}
	return false
}
