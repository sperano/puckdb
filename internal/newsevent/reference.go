package newsevent

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/internal/llm"
)

// referenceClient answers each corpus article with its labeled reference
// reply, keyed by the rendered input. It stands in for a model when the
// harness itself is tested, and shows what a correct model returns.
type referenceClient struct {
	replies map[string]string
}

// ReferenceClient returns a client that replays the corpus's reference
// replies. Inputs are rendered as Evaluate renders them with maxInputRunes.
func ReferenceClient(c Corpus, maxInputRunes int) llm.Client {
	rc := referenceClient{replies: make(map[string]string)}
	for _, tc := range c.Cases {
		cr := newCaseRun(tc)
		for i, step := range tc.Steps {
			in, _ := cr.step(i, maxInputRunes)
			rc.replies[UserMessage(in)] = step.Reply
		}
	}
	return rc
}

// Complete returns the reference reply of the rendered article.
func (rc referenceClient) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	for _, m := range req.Messages {
		if reply, ok := rc.replies[m.Content]; ok && m.Role == "user" {
			return &llm.Response{Content: reply, Usage: &llm.Usage{}}, nil
		}
	}
	return nil, fmt.Errorf("no reference reply for this input")
}
