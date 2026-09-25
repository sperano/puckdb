package news

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var itemBaseTime = time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)

func baseItem() Item {
	return Item{
		Title: "Brayden McNabb suspended three games", Text: "The NHL suspended Brayden McNabb.",
		Author: "Staff", PublishedAt: itemBaseTime,
		Subjects:     []Subject{{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"}},
		TeamHints:    []string{"VGK"},
		CategoryHint: CategorySuspension,
		URL:          "https://nhl.com/a", UpdatedAt: itemBaseTime, RetrievedAt: itemBaseTime,
	}
}

func TestContentHashIgnoresUpdatedRetrievedAndURL(t *testing.T) {
	base := baseItem()
	changed := base
	changed.UpdatedAt = base.UpdatedAt.Add(time.Hour)
	changed.RetrievedAt = base.RetrievedAt.Add(2 * time.Hour)
	changed.URL = "https://nhl.com/b"
	assert.Equal(t, mustHash(t, base), mustHash(t, changed))
}

func TestContentHashChangesWithContentFields(t *testing.T) {
	base := baseItem()
	tests := map[string]func(Item) Item{
		"title": func(it Item) Item { it.Title += "!"; return it },
		"text":  func(it Item) Item { it.Text += "!"; return it },
		"publishedAt": func(it Item) Item {
			it.PublishedAt = it.PublishedAt.Add(time.Minute)
			return it
		},
		"subjects": func(it Item) Item {
			it.Subjects = append(it.Subjects, Subject{NHLPlayerID: ferraroID, Name: "Mario Ferraro"})
			return it
		},
		"teamHints": func(it Item) Item { it.TeamHints = append(it.TeamHints, "LAK"); return it },
		"categoryHint": func(it Item) Item {
			it.CategoryHint = CategoryInjury
			return it
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			assert.NotEqual(t, mustHash(t, base), mustHash(t, mutate(base)))
		})
	}
}

func TestContentHashSubjectOrderIsStableAfterNormalization(t *testing.T) {
	a := normalizeItem(Item{
		Title: "Trade", Text: "Two players traded.",
		Subjects: []Subject{{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"}, {NHLPlayerID: ferraroID, Name: "Mario Ferraro"}},
	})
	b := normalizeItem(Item{
		Title: "Trade", Text: "Two players traded.",
		Subjects: []Subject{{NHLPlayerID: ferraroID, Name: "Mario Ferraro"}, {NHLPlayerID: mcnabbID, Name: "Brayden McNabb"}},
	})
	assert.Equal(t, mustHash(t, a), mustHash(t, b))
}

func TestTitleFingerprintIgnoresCaseAccentsAndPunctuation(t *testing.T) {
	a := Item{Title: "Brayden McNabb suspended three games!"}
	b := Item{Title: "brayden mcnabb SUSPENDED three games"}
	fingerprint := a.TitleFingerprint()
	assert.NotEmpty(t, fingerprint)
	assert.Equal(t, fingerprint, b.TitleFingerprint())
}

func TestTitleFingerprintEmptyBelowMinimumWords(t *testing.T) {
	it := Item{Title: "Short title"}
	assert.Empty(t, it.TitleFingerprint())
}

func TestReportedAtPrecedence(t *testing.T) {
	published := itemBaseTime
	updated := itemBaseTime.Add(time.Hour)
	retrieved := itemBaseTime.Add(2 * time.Hour)

	assert.Equal(t, published, ReportedAt(published, updated, retrieved))
	assert.Equal(t, updated, ReportedAt(time.Time{}, updated, retrieved))
	assert.Equal(t, retrieved, ReportedAt(time.Time{}, time.Time{}, retrieved))
}

func TestCleanTextStripsTagsDecodesEntitiesAndCollapsesWhitespace(t *testing.T) {
	// A tag is replaced with a space, so one immediately touching punctuation
	// (</b>.) leaves that punctuation as its own token; CleanText only
	// collapses whitespace, it does not rejoin punctuation to words.
	raw := "<p>Ferraro &amp; McNabb\n\n  were   <b>traded</b> today.</p>"
	assert.Equal(t, "Ferraro & McNabb were traded today.", CleanText(raw, 1000))
}

func TestCleanTextTruncatesAtWordBoundary(t *testing.T) {
	raw := strings.Repeat("word ", 20)
	out := CleanText(raw, 12)
	assert.Equal(t, "word word"+evidenceEllipsis, out, "must cut at the space, not mid-word")
}

func TestCleanTextNoTruncationBelowMax(t *testing.T) {
	assert.Equal(t, "short text", CleanText("short text", 1000))
}

func TestWords(t *testing.T) {
	assert.Equal(t, []string{"brayden", "mcnabb"}, Words("Brayden McNabb!"))
	assert.Equal(t, []string{"alex", "lyon"}, Words("Álex Lyón"))
	assert.Empty(t, Words(""))
}

func TestNormalizeItemExternalIDFallsBackToURL(t *testing.T) {
	it := normalizeItem(Item{Title: "T", URL: "https://nhl.com/a"})
	assert.Equal(t, "https://nhl.com/a", it.ExternalID)
}

func TestNormalizeItemKeepsExplicitExternalID(t *testing.T) {
	it := normalizeItem(Item{Title: "T", ExternalID: " abc123 ", URL: "https://nhl.com/a"})
	assert.Equal(t, "abc123", it.ExternalID)
}

func TestNormalizeItemUppercasesDedupesAndSortsTeamHints(t *testing.T) {
	it := normalizeItem(Item{Title: "T", TeamHints: []string{"vgk", "LAK", " vgk ", "buf"}})
	assert.Equal(t, []string{"BUF", "LAK", "VGK"}, it.TeamHints)
}

func mustHash(t *testing.T, it Item) string {
	t.Helper()
	hash, err := it.ContentHash()
	require.NoError(t, err)
	return hash
}

func TestContentHashRejectsUnencodableTime(t *testing.T) {
	const outOfRangeYear = 10000
	_, err := Item{ExternalID: "x", PublishedAt: time.Date(outOfRangeYear, time.January, 1, 0, 0, 0, 0, time.UTC)}.ContentHash()
	require.Error(t, err, "a time JSON cannot encode must not collapse every item onto one hash")
}

func TestContentHashChangesWithBody(t *testing.T) {
	base := baseItem()
	withBody := base
	withBody.Body = "The full story."
	corrected := withBody
	corrected.Body = "The full, corrected story."
	assert.NotEqual(t, mustHash(t, base), mustHash(t, withBody))
	assert.NotEqual(t, mustHash(t, withBody), mustHash(t, corrected))
}

func TestContentHashWithoutBodyIsUnchanged(t *testing.T) {
	// hashedContent as it was before bodies were stored: a version with no
	// body must keep its hash, or every stored story would be reprocessed.
	type legacyHashedContent struct {
		Title        string    `json:"title"`
		Text         string    `json:"text"`
		Author       string    `json:"author"`
		PublishedAt  time.Time `json:"publishedAt"`
		Subjects     []Subject `json:"subjects"`
		TeamHints    []string  `json:"teamHints"`
		CategoryHint Category  `json:"categoryHint"`
	}
	it := baseItem()
	data, err := json.Marshal(legacyHashedContent{
		Title: it.Title, Text: it.Text, Author: it.Author, PublishedAt: it.PublishedAt.UTC(),
		Subjects: it.Subjects, TeamHints: it.TeamHints, CategoryHint: it.CategoryHint,
	})
	require.NoError(t, err)
	assert.Equal(t, hashHex(data), mustHash(t, it))
}

func TestCleanBodyKeepsParagraphsAndDropsCode(t *testing.T) {
	raw := `<div class="sn-video"><script>var permalink = "x";</script></div>
<p>The <b>Canucks</b> acquired Mario Ferraro &amp; a pick.</p>

<style>.a { color: red; }</style><p>He   signed<br>a two-year deal.</p>`
	assert.Equal(t, "The Canucks acquired Mario Ferraro & a pick.\n\nHe signed\na two-year deal.", CleanBody(raw, MaxBodyRunes))
}

func TestCleanBodyOfOnlyCodeIsEmpty(t *testing.T) {
	assert.Empty(t, CleanBody(`<p><div id="v"></div><script type="text/javascript">play();</script></p>`, MaxBodyRunes))
}

func TestCleanBodyTruncatesAtAWord(t *testing.T) {
	const maxRunes = 16
	assert.Equal(t, "Mario Ferraro"+evidenceEllipsis, CleanBody("Mario Ferraro signed today", maxRunes))
}

func TestNormalizeItemDropsABodyThatRepeatsTheSummary(t *testing.T) {
	it := normalizeItem(Item{Title: "T", Text: "<p>Short summary.</p>", Body: "<p>Short\n summary.</p>"})
	assert.Equal(t, "Short summary.", it.Text)
	assert.Empty(t, it.Body)

	longer := normalizeItem(Item{Title: "T", Text: "Short summary.", Body: "<p>Short summary.</p><p>More.</p>"})
	assert.Equal(t, "Short summary.\n\nMore.", longer.Body)
}
