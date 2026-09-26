package newsevent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requiredScenarios are the acceptance scenarios the corpus must cover.
var requiredScenarios = []string{
	"Hellebuyck-style indefinite team suspension",
	"fixed-length suspension",
	"ambiguous injury duration",
	"conflicting reports",
	"a rumor",
	"a correction",
	"reinstatement",
	"malicious article instructions",
	"duplicate articles",
}

func TestBuiltinCorpusCoversTheAcceptanceScenarios(t *testing.T) {
	c, err := LoadCorpus("")
	require.NoError(t, err)
	assert.NotEmpty(t, c.Version)
	covered := make(map[string]bool)
	for _, tc := range c.Cases {
		covered[tc.Covers] = true
	}
	for _, scenario := range requiredScenarios {
		assert.True(t, covered[scenario], "no case covers %q", scenario)
	}
}

func writeCorpus(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadCorpusFromFile(t *testing.T) {
	path := writeCorpus(t, `
version: test-v1
cases:
  - id: one
    players: [{ref: P1, name: A Player, nhl_id: 1}]
    steps:
      - {article: a, publisher: X, kind: reporting, published: 2026-10-01T00:00:00Z, text: Some text.}
`)
	c, err := LoadCorpus(path)
	require.NoError(t, err)
	assert.Equal(t, "test-v1", c.Version)
	require.Len(t, c.Cases, 1)
	assert.Equal(t, "one", c.Cases[0].ID)
}

func TestLoadCorpusRejectsIncompleteCorpora(t *testing.T) {
	cases := map[string]string{
		"no version":  "cases: [{id: a}]\n",
		"no cases":    "version: v\n",
		"no steps":    "version: v\ncases: [{id: a, players: [{ref: P1}]}]\n",
		"no players":  "version: v\ncases: [{id: a, steps: [{article: x, published: 2026-10-01T00:00:00Z, text: t}]}]\n",
		"duplicate":   "version: v\ncases:\n" + corpusCaseYAML("a") + corpusCaseYAML("a"),
		"no article":  "version: v\ncases: [{id: a, players: [{ref: P1}], steps: [{published: 2026-10-01T00:00:00Z, text: t}]}]\n",
		"no text":     "version: v\ncases: [{id: a, players: [{ref: P1}], steps: [{article: x, published: 2026-10-01T00:00:00Z}]}]\n",
		"not yaml":    "version: [unclosed\n",
		"no pubdate":  "version: v\ncases: [{id: a, players: [{ref: P1}], steps: [{article: x, text: t}]}]\n",
		"bad pubdate": "version: v\ncases: [{id: a, players: [{ref: P1}], steps: [{article: x, published: soon, text: t}]}]\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadCorpus(writeCorpus(t, content))
			assert.Error(t, err)
		})
	}
}

func corpusCaseYAML(id string) string {
	return "  - {id: " + id + ", players: [{ref: P1}], steps: [{article: x, published: 2026-10-01T00:00:00Z, text: t}]}\n"
}

func TestLoadCorpusMissingFile(t *testing.T) {
	_, err := LoadCorpus(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.Error(t, err)
}
