package newsevent

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/news"
)

// IssueCode names what validation dropped or cleared.
type IssueCode string

const (
	// IssueUnknownPlayer: the event names a player ref that was not given.
	IssueUnknownPlayer IssueCode = "unknown_player"
	// IssueInvalidValue: a value outside the schema's enumerations or
	// formats.
	IssueInvalidValue IssueCode = "invalid_value"
	// IssueMissingEvidence: an event with no usable quote.
	IssueMissingEvidence IssueCode = "missing_evidence"
	// IssueUnknownDocument: a quote cites a document that was not given.
	IssueUnknownDocument IssueCode = "unknown_document"
	// IssueQuoteNotFound: a quote that is not verbatim in the document, or
	// too short to prove anything.
	IssueQuoteNotFound IssueCode = "quote_not_found"
	// IssueUnsupportedClaim: a value its quote does not state; the value is
	// cleared to unknown.
	IssueUnsupportedClaim IssueCode = "unsupported_claim"
	// IssueChronology: a date out of order or implausibly far from the
	// report; the value is cleared.
	IssueChronology IssueCode = "chronology"
	// IssueSuspectedInstructions: the article contains text addressed to a
	// language model.
	IssueSuspectedInstructions IssueCode = "suspected_instructions"
	// IssueQuoteFromInstructions: a quote taken from such text.
	IssueQuoteFromInstructions IssueCode = "quote_from_instructions"
)

const (
	// outputLevel is the Issue.Event of an issue about the whole reply.
	outputLevel = -1
	// maxEffectivePast and maxEffectiveFuture bound an effective date
	// around the report date: news older than a year is not current, and a
	// start more than two months out is not what a report announces.
	maxEffectivePast   = 365 * hoursPerDay * time.Hour
	maxEffectiveFuture = 60 * hoursPerDay * time.Hour
	// maxUntilFuture bounds a stated end date (a season-long absence).
	maxUntilFuture = 400 * hoursPerDay * time.Hour
	// maxStatedGames and maxStatedDays bound stated lengths.
	maxStatedGames = 100
	maxStatedDays  = 400
	// reviewSuspectedInstructions is the review reason of events from an
	// article with suspected instructions.
	reviewSuspectedInstructions = "article contains text addressed to a language model"
)

// Issue is one thing validation dropped or cleared.
type Issue struct {
	Code IssueCode `json:"code"`
	// Event is the event's index in the reply, or -1 for the whole reply.
	Event   int    `json:"event"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Validation is a reply checked against its input.
type Validation struct {
	Events []Event `json:"events"`
	Issues []Issue `json:"issues"`
	// Claims counts what the reply asserted: each event, quote and factual
	// value. Unsupported counts those dropped or cleared for lack of
	// support.
	Claims      int  `json:"claims"`
	Unsupported int  `json:"unsupported"`
	Suspicious  bool `json:"suspicious"`
}

// Clean reports whether validation kept the reply as it was.
func (v Validation) Clean() bool {
	return len(v.Issues) == 0
}

// Interpret decodes and validates a reply. It fails only when the reply
// does not follow the schema; problems with single events or values are
// issues.
func Interpret(reply string, in Input) (Validation, error) {
	raw, err := decodeOutput(reply)
	if err != nil {
		return Validation{}, err
	}
	return Validate(raw, in), nil
}

// validator checks one reply.
type validator struct {
	players map[string]news.Identity
	docs    map[string]docIndex
	v       Validation
}

// Validate keeps the events whose player was given and whose quotes are in
// the documents, and clears every value its quote does not state.
func Validate(events []rawEvent, in Input) Validation {
	c := validator{players: make(map[string]news.Identity), docs: make(map[string]docIndex)}
	for _, p := range in.Players {
		c.players[p.Ref] = p.Identity
	}
	for _, d := range in.Documents {
		idx := indexDocument(d)
		c.docs[d.Ref] = idx
		if idx.hasInstructions() && !c.v.Suspicious {
			c.v.Suspicious = true
			c.issue(outputLevel, IssueSuspectedInstructions, "", "document %s contains text addressed to a language model", d.Ref)
		}
	}
	c.v.Events = []Event{}
	for i, raw := range events {
		c.event(i, raw)
	}
	return c.v
}

func (c *validator) issue(event int, code IssueCode, field, format string, args ...any) {
	c.v.Issues = append(c.v.Issues, Issue{Code: code, Event: event, Field: field, Message: fmt.Sprintf(format, args...)})
}

// unsupported records a dropped or cleared claim.
func (c *validator) unsupported(event int, code IssueCode, field, format string, args ...any) {
	c.v.Unsupported++
	c.issue(event, code, field, format, args...)
}

func (c *validator) event(i int, raw rawEvent) {
	c.v.Claims++
	player, ok := c.players[raw.Player]
	if !ok {
		c.unsupported(i, IssueUnknownPlayer, "player", "player %q was not given", raw.Player)
		return
	}
	typ, status := Type(raw.Type), ReportStatus(raw.ReportStatus)
	if !validTypes[typ] || !validStatuses[status] {
		c.unsupported(i, IssueInvalidValue, "type", "type %q or report_status %q is not in the schema", raw.Type, raw.ReportStatus)
		return
	}
	quotes, doc, ok := c.evidence(i, raw.Evidence)
	if !ok {
		return
	}
	e := Event{Player: player, Type: typ, Status: status, Evidence: quotes, Duration: Duration{Kind: DurationUnknown}}
	if status != StatusDenied {
		e.Attribution = c.attribution(i, raw.Attribution)
		e.EffectiveOn = c.effective(i, raw.EffectiveFrom, doc.ReportedAt)
		e.Duration = c.duration(i, raw.Duration, doc.ReportedAt, e.EffectiveOn)
		e.Change = c.change(i, raw.Change)
	}
	if c.v.Suspicious {
		e.NeedsReview, e.ReviewReason = true, reviewSuspectedInstructions
	}
	c.add(e)
}

// evidence keeps the quotes that are verbatim in a given document and not
// taken from suspected instructions. The event is dropped when none is left.
func (c *validator) evidence(i int, raw []rawQuote) ([]Quote, Document, bool) {
	var quotes []Quote
	var first Document
	for _, q := range raw {
		c.v.Claims++
		idx, ok := c.docs[q.Doc]
		if !ok {
			c.unsupported(i, IssueUnknownDocument, "evidence", "document %q was not given", q.Doc)
			continue
		}
		if !c.quoteUsable(i, idx, q.Quote, "evidence") {
			continue
		}
		if len(quotes) == 0 {
			first = idx.doc
		}
		quotes = append(quotes, Quote{Doc: q.Doc, Text: q.Quote})
	}
	if len(quotes) == 0 {
		c.unsupported(i, IssueMissingEvidence, "evidence", "no quote from the documents supports the event")
		return nil, Document{}, false
	}
	return quotes, first, true
}

// quoteUsable reports whether quote is verbatim in idx, long enough to
// prove something, and not taken from suspected instructions.
func (c *validator) quoteUsable(i int, idx docIndex, quote, field string) bool {
	if len(news.Words(quote)) < minQuoteWords {
		c.unsupported(i, IssueQuoteNotFound, field, "quote %q is shorter than %d words", quote, minQuoteWords)
		return false
	}
	found, suspicious := idx.find(quote)
	switch {
	case !found:
		c.unsupported(i, IssueQuoteNotFound, field, "quote %q is not in document %s", quote, idx.doc.Ref)
		return false
	case suspicious:
		c.unsupported(i, IssueQuoteFromInstructions, field, "quote %q comes from suspected instructions", quote)
		return false
	}
	return true
}

// quoted reports whether quote is usable in any document.
func (c *validator) quoted(i int, quote, field string) bool {
	for _, idx := range c.docs {
		if found, suspicious := idx.find(quote); found && !suspicious && len(news.Words(quote)) >= minQuoteWords {
			return true
		}
	}
	c.unsupported(i, IssueQuoteNotFound, field, "quote %q is not usable evidence from the documents", quote)
	return false
}

func (c *validator) attribution(i int, attribution string) string {
	if len(news.Words(attribution)) == 0 {
		return ""
	}
	c.v.Claims++
	for _, idx := range c.docs {
		if idx.mentions(attribution) {
			return attribution
		}
	}
	c.unsupported(i, IssueUnsupportedClaim, "attribution", "attribution %q is not in the documents", attribution)
	return ""
}

// add appends an event, merging the evidence of a repeated claim.
func (c *validator) add(e Event) {
	for k, prev := range c.v.Events {
		if prev.sameClaim(e) {
			c.v.Events[k].Evidence = mergeQuotes(prev.Evidence, e.Evidence)
			return
		}
	}
	c.v.Events = append(c.v.Events, e)
}

func mergeQuotes(a, b []Quote) []Quote {
	out := append([]Quote(nil), a...)
	for _, q := range b {
		seen := false
		for _, p := range out {
			seen = seen || (p.Doc == q.Doc && normalize(p.Text) == normalize(q.Text))
		}
		if !seen {
			out = append(out, q)
		}
	}
	return out
}
