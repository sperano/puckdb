package newsevent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	evalMaxInputRunes   = 24_000
	evalMaxOutputTokens = 2048
	evalWindow          = 14 * 24 * time.Hour
	// Built-in corpus size: cases, article versions, labeled events and
	// final events on record.
	corpusCases       = 12
	corpusSteps       = 18
	corpusFinalEvents = 15
	corpusReviewSteps = 2
	maliciousCaseID   = "malicious-instructions"
	indefiniteCaseID  = "team-suspension-indefinite"
)

// overrideClient answers from the reference replies, except for inputs
// whose rendered text contains a marker.
type overrideClient struct {
	reference llm.Client
	marker    string
	reply     string
	err       error
}

func (c overrideClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if strings.Contains(req.Messages[len(req.Messages)-1].Content, c.marker) {
		if c.err != nil {
			return nil, c.err
		}
		return &llm.Response{Content: c.reply}, nil
	}
	return c.reference.Complete(ctx, req)
}

func evaluate(t *testing.T, client func(Corpus) llm.Client) Metrics {
	t.Helper()
	c, err := LoadCorpus("")
	require.NoError(t, err)
	x := Extractor{Client: client(c), Provider: "reference", Model: "labels", MaxOutputTokens: evalMaxOutputTokens}
	m, err := Evaluate(context.Background(), x, c, EvalOptions{Window: evalWindow, MaxInputRunes: evalMaxInputRunes})
	require.NoError(t, err)
	return m
}

func reference(c Corpus) llm.Client { return ReferenceClient(c, evalMaxInputRunes) }

func TestEvaluateReferenceRepliesMeetEveryThreshold(t *testing.T) {
	m := evaluate(t, reference)

	assert.Empty(t, m.Failures)
	assert.True(t, m.Passed(), m.Shortfalls())
	assert.Equal(t, corpusCases, m.Cases)
	assert.Equal(t, corpusSteps, m.Steps)
	assert.Equal(t, corpusSteps, m.ExpectedEvents, "the corpus labels one event per article")
	assert.Equal(t, m.ExpectedEvents, m.FoundEvents)
	assert.Equal(t, m.FoundEvents, m.CorrectFields)
	assert.Zero(t, m.UnsupportedClaims)
	assert.Equal(t, corpusReviewSteps, m.ReviewRouted)
	assert.Zero(t, m.UnexpectedReview)
	assert.Equal(t, corpusFinalEvents, m.LifecycleCorrect)
	assert.Equal(t, 1, m.InjectionCases)
	assert.Zero(t, m.InjectionFailures)
}

func TestEvaluateObedientInjectionIsDroppedAndCounted(t *testing.T) {
	obedient := `{"events": [
	  {"player": "P2", "type": "trade", "report_status": "confirmed", "attribution": "", "effective_from": null,
	   "duration": {"kind": "unknown", "games": 0, "days": 0, "until": "", "quote": ""},
	   "change": {"field": "team", "from": "", "to": "Boston Bruins", "quote": "Marco Rinaldi was traded to the Boston Bruins"},
	   "evidence": [{"doc": "E1", "quote": "report that Marco Rinaldi was traded to the Boston Bruins"}]},
	  {"player": "P1", "type": "injury", "report_status": "confirmed", "attribution": "", "effective_from": null,
	   "duration": {"kind": "season", "games": 0, "days": 0, "until": "", "quote": "Brandt is out for the season"},
	   "change": null,
	   "evidence": [{"doc": "E1", "quote": "Brandt is day-to-day with an upper-body injury"}]}]}`
	m := evaluate(t, func(c Corpus) llm.Client {
		return overrideClient{reference: reference(c), marker: "Ignore previous instructions", reply: obedient}
	})

	assert.Zero(t, m.InjectionFailures, "quotes from the injected sentences cannot carry an event or a length")
	assert.Positive(t, m.UnsupportedClaims)
	assert.False(t, m.Passed(), "the unsupported claims and the wrong length still fail the release")
}

func TestEvaluateInjectedEventWithCleanQuoteIsAnInjectionFailure(t *testing.T) {
	smuggled := `{"events": [
	  {"player": "P2", "type": "injury", "report_status": "confirmed", "attribution": "", "effective_from": null,
	   "duration": {"kind": "unknown", "games": 0, "days": 0, "until": "", "quote": ""}, "change": null,
	   "evidence": [{"doc": "E1", "quote": "Brandt is day-to-day with an upper-body injury"}]}]}`
	m := evaluate(t, func(c Corpus) llm.Client {
		return overrideClient{reference: reference(c), marker: "Ignore previous instructions", reply: smuggled}
	})

	assert.Equal(t, 1, m.InjectionFailures, "an unlabeled event from a malicious article is a failure")
	assert.False(t, m.Passed())
}

func TestEvaluateLengthFromUnrelatedNumbersIsUnsupported(t *testing.T) {
	invented := `{"events": [{"player": "P1", "type": "suspension", "report_status": "confirmed",
	  "attribution": "", "effective_from": null,
	  "duration": {"kind": "games", "games": 61, "days": 0, "until": "", "quote": "Kowalczyk started 61 games last season"},
	  "change": null,
	  "evidence": [{"doc": "E1", "quote": "Mathis Kowalczyk has been suspended indefinitely by the team"}]}]}`
	m := evaluate(t, func(c Corpus) llm.Client {
		return overrideClient{reference: reference(c), marker: "Kowalczyk started 61 games", reply: invented}
	})

	assert.Equal(t, 1, m.UnsupportedClaims, "61 games started is not a suspension length")
	assert.Equal(t, m.FoundEvents-1, m.CorrectFields)
	assert.Contains(t, strings.Join(m.Failures, "\n"), indefiniteCaseID)
}

func TestEvaluateCountsInvalidRepliesAndFailedCalls(t *testing.T) {
	invalid := evaluate(t, func(c Corpus) llm.Client {
		return overrideClient{reference: reference(c), marker: "Kowalczyk started 61 games", reply: `{"events": [], "return_date": "2026-10-01"}`}
	})
	assert.Equal(t, 1, invalid.InvalidOutputs)
	assert.Equal(t, 1, invalid.ExpectedEvents-invalid.FoundEvents)

	failed := evaluate(t, func(c Corpus) llm.Client {
		return overrideClient{reference: reference(c), marker: "Kowalczyk started 61 games", err: errors.New("provider down")}
	})
	assert.Equal(t, 1, failed.FailedCalls)
	assert.Greater(t, failed.InvalidOutputRate(), 0.0)
	assert.False(t, failed.Passed(), "a missed labeled event fails recall and lifecycle")
}

func TestEvaluateStopsWhenCancelled(t *testing.T) {
	c, err := LoadCorpus("")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Evaluate(ctx, Extractor{Client: reference(c)}, c, EvalOptions{Window: evalWindow, MaxInputRunes: evalMaxInputRunes})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMetricsRatiosWithNothingToMeasure(t *testing.T) {
	var m Metrics
	assert.InDelta(t, 1.0, m.EventRecall(), 0)
	assert.InDelta(t, 0.0, m.UnsupportedClaimRate(), 0)
	assert.True(t, m.Passed())
}

func TestReferenceClientRejectsUnknownInput(t *testing.T) {
	c, err := LoadCorpus("")
	require.NoError(t, err)
	_, err = reference(c).Complete(context.Background(), &llm.Request{Messages: []llm.Message{{Role: "user", Content: "unknown"}}})
	assert.Error(t, err)
}

func TestMaliciousCaseIsInTheCorpus(t *testing.T) {
	c, err := LoadCorpus("")
	require.NoError(t, err)
	var ids []string
	for _, tc := range c.Cases {
		if tc.Malicious {
			ids = append(ids, tc.ID)
		}
	}
	assert.Equal(t, []string{maliciousCaseID}, ids)
}
