package newsevent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// maxEventsPerOutput bounds one reply; more is a runaway output.
	maxEventsPerOutput = 20
	// codeFence wraps replies of models that format JSON as Markdown.
	codeFence     = "```"
	codeFenceJSON = "```json"
)

// ErrInvalidOutput marks a reply that does not follow the schema at all:
// not one JSON object, fields the schema does not have (a "return_date",
// a "fantasy_impact"), too many events, or tool calls.
var ErrInvalidOutput = errors.New("invalid model output")

// rawOutput is the reply schema. Decoding rejects unknown fields.
type rawOutput struct {
	Events *[]rawEvent `json:"events"`
}

type rawEvent struct {
	Player        string       `json:"player"`
	Type          string       `json:"type"`
	ReportStatus  string       `json:"report_status"`
	Attribution   string       `json:"attribution"`
	EffectiveFrom *rawDate     `json:"effective_from"`
	Duration      *rawDuration `json:"duration"`
	Change        *rawChange   `json:"change"`
	Evidence      []rawQuote   `json:"evidence"`
}

type rawDate struct {
	Date  string `json:"date"`
	Quote string `json:"quote"`
}

type rawDuration struct {
	Kind  string `json:"kind"`
	Games int    `json:"games"`
	Days  int    `json:"days"`
	Until string `json:"until"`
	Quote string `json:"quote"`
}

type rawChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
	Quote string `json:"quote"`
}

type rawQuote struct {
	Doc   string `json:"doc"`
	Quote string `json:"quote"`
}

// decodeOutput parses a reply strictly: one JSON object with an events list
// and no field the schema lacks. A Markdown code fence around the object is
// tolerated; any other text is not.
func decodeOutput(reply string) ([]rawEvent, error) {
	body := stripFence(strings.TrimSpace(reply))
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var out rawOutput
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: text after the JSON object", ErrInvalidOutput)
	}
	if out.Events == nil {
		return nil, fmt.Errorf("%w: no events list", ErrInvalidOutput)
	}
	if len(*out.Events) > maxEventsPerOutput {
		return nil, fmt.Errorf("%w: %d events, at most %d allowed", ErrInvalidOutput, len(*out.Events), maxEventsPerOutput)
	}
	return *out.Events, nil
}

func stripFence(s string) string {
	if !strings.HasPrefix(s, codeFence) || !strings.HasSuffix(s, codeFence) || len(s) < 2*len(codeFence) {
		return s
	}
	inner := strings.TrimSuffix(s, codeFence)
	if strings.HasPrefix(inner, codeFenceJSON) {
		inner = strings.TrimPrefix(inner, codeFenceJSON)
	} else {
		inner = strings.TrimPrefix(inner, codeFence)
	}
	return strings.TrimSpace(inner)
}
