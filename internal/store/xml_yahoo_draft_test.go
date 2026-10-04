package store

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDraftResultList_PreservesCountEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		xml      string
		hasCount bool
		count    int
	}{
		{name: "explicit empty", xml: `<draft_results count="0"></draft_results>`, hasCount: true},
		{name: "attribute omitted", xml: `<draft_results></draft_results>`},
		{name: "positive", xml: `<draft_results count="2"><draft_result/><draft_result/></draft_results>`, hasCount: true, count: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got DraftResultList
			require.NoError(t, xml.Unmarshal([]byte(test.xml), &got))
			assert.Equal(t, test.hasCount, got.HasCount)
			assert.Equal(t, test.count, got.Count)
		})
	}
}
