package newsevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// EvaluationQueries records and reads evaluation runs.
type EvaluationQueries interface {
	InsertNewsExtractionEvaluation(ctx context.Context, arg sqlcdb.InsertNewsExtractionEvaluationParams) (int64, error)
	GetLatestNewsExtractionEvaluation(ctx context.Context, arg sqlcdb.GetLatestNewsExtractionEvaluationParams) (sqlcdb.NewsExtractionEvaluation, error)
}

// RecordEvaluation stores an evaluation run of an extractor on a corpus.
func RecordEvaluation(ctx context.Context, q EvaluationQueries, extractorKey string, c Corpus, m Metrics, now time.Time) (int64, error) {
	metrics, err := json.Marshal(m)
	if err != nil {
		return 0, fmt.Errorf("encode evaluation metrics: %w", err)
	}
	id, err := q.InsertNewsExtractionEvaluation(ctx, sqlcdb.InsertNewsExtractionEvaluationParams{
		ExtractorKey: extractorKey, CorpusVersion: c.Version, Cases: int32(m.Cases), Passed: m.Passed(),
		Metrics: metrics, RunAt: news.Timestamptz(now),
	})
	if err != nil {
		return 0, fmt.Errorf("record news extraction evaluation: %w", err)
	}
	return id, nil
}

// AutomaticEffectsAllowed reports whether events of an extractor may drive
// automatic numeric effects: its latest evaluation on the built-in corpus
// must have passed every release threshold. Events routed to review are
// never fit for automatic use, whatever this says.
func AutomaticEffectsAllowed(ctx context.Context, q EvaluationQueries, extractorKey string) (bool, error) {
	c, err := LoadCorpus("")
	if err != nil {
		return false, err
	}
	run, err := q.GetLatestNewsExtractionEvaluation(ctx, sqlcdb.GetLatestNewsExtractionEvaluationParams{
		ExtractorKey: extractorKey, CorpusVersion: c.Version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load news extraction evaluation: %w", err)
	}
	return run.Passed, nil
}

// WriteEvaluation renders an evaluation run as Markdown.
func WriteEvaluation(w io.Writer, extractorKey, corpusVersion string, m Metrics) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# News event extraction evaluation\n\nExtractor `%s`, corpus `%s`: %d cases, %d articles, %d prompt and %d completion tokens.\n\n",
		extractorKey, corpusVersion, m.Cases, m.Steps, m.PromptTokens, m.CompletionTokens)
	b.WriteString("| Measure | Value | Release threshold |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| Event recall | %.3f (%d/%d) | ≥ %.2f |\n", m.EventRecall(), m.FoundEvents, m.ExpectedEvents, MinEventRecall)
	fmt.Fprintf(&b, "| Event precision | %.3f (%d/%d) | ≥ %.2f |\n", m.EventPrecision(), m.FoundEvents, m.EmittedEvents, MinEventPrecision)
	fmt.Fprintf(&b, "| Field accuracy | %.3f (%d/%d) | ≥ %.2f |\n", m.FieldAccuracy(), m.CorrectFields, m.FoundEvents, MinFieldAccuracy)
	fmt.Fprintf(&b, "| Unsupported-claim rate | %.3f (%d/%d) | ≤ %.2f |\n", m.UnsupportedClaimRate(), m.UnsupportedClaims, m.Claims, MaxUnsupportedClaimRate)
	fmt.Fprintf(&b, "| Invalid-output rate | %.3f (%d invalid, %d failed calls / %d) | ≤ %.2f |\n",
		m.InvalidOutputRate(), m.InvalidOutputs, m.FailedCalls, m.Steps, MaxInvalidOutputRate)
	fmt.Fprintf(&b, "| Review recall | %.3f (%d/%d) | ≥ %.2f |\n", m.ReviewRecall(), m.ReviewRouted, m.ReviewExpected, MinReviewRecall)
	fmt.Fprintf(&b, "| Lifecycle accuracy | %.3f (%d/%d) | ≥ %.2f |\n", m.LifecycleAccuracy(), m.LifecycleCorrect, m.LifecycleExpected, MinLifecycleAccuracy)
	fmt.Fprintf(&b, "| Injection failures | %d of %d cases | ≤ %d |\n", m.InjectionFailures, m.InjectionCases, MaxInjectionFailures)
	fmt.Fprintf(&b, "| Unexpected reviews | %d | informational |\n\n", m.UnexpectedReview)
	if shortfalls := m.Shortfalls(); len(shortfalls) > 0 {
		fmt.Fprintf(&b, "**Failed:** %s. Automatic numeric effects stay off for this extractor.\n\n", strings.Join(shortfalls, "; "))
	} else {
		b.WriteString("**Passed** every release threshold.\n\n")
	}
	if len(m.Failures) > 0 {
		b.WriteString("## Misses\n\n")
		for _, f := range m.Failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
