package news

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sperano/puckdb/internal/matching"
)

const (
	// MaxEvidenceRunes bounds the evidence text kept per version: the
	// publisher's own summary, which reports and classification read.
	MaxEvidenceRunes = 1000
	// MaxBodyRunes bounds the full story text kept per version; the longest
	// features seen (Sportsnet rankings) clean to well under this.
	MaxBodyRunes = 200_000
	// maxTitleRunes bounds a stored title.
	maxTitleRunes = 300
	// evidenceEllipsis marks evidence text that was cut.
	evidenceEllipsis = "…"
	// minFingerprintWords is the fewest words a title or text needs before
	// its fingerprint may identify a syndicated copy; shorter strings
	// ("Injury update") are too generic to prove two stories are one.
	minFingerprintWords = 5
)

var (
	htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
	// htmlCodePattern matches script and style elements, whose content is
	// code rather than story text (Sportsnet embeds video players).
	htmlCodePattern = regexp.MustCompile(`(?is)<script\b.*?</script\s*>|<style\b.*?</style\s*>`)
	// htmlBlockTagPattern matches tags that end a line of text.
	htmlBlockTagPattern = regexp.MustCompile(
		`(?i)<(?:br|/?p|/?div|/?h[1-6]|/?li|/?ul|/?ol|/?tr|/?table|/?blockquote|/?section|/?article|/?figure|/?header|/?footer)\b[^>]*>`)
)

// Subject is a player a source itself names in structured form.
type Subject struct {
	NHLPlayerID   int64  `json:"nhlPlayerId,omitempty"`
	YahooPlayerID int    `json:"yahooPlayerId,omitempty"`
	Name          string `json:"name,omitempty"`
	// InBody marks a player the story's body links inline rather than one
	// the story is tagged with.
	InBody bool `json:"inBody,omitempty"`
}

// Item is one story or status entry read from a source.
type Item struct {
	ExternalID string
	URL        string
	Title      string
	// Text is the publisher's summary (bounded evidence text).
	Text string
	// Body is the full story text when the source offers more than its
	// summary; empty otherwise.
	Body string
	// BodyURL is where a fetch_body source serves the full story; it is not
	// stored.
	BodyURL string
	Author  string
	// PublishedAt and UpdatedAt are the source's own timestamps; zero when
	// the source gives none.
	PublishedAt time.Time
	UpdatedAt   time.Time
	RetrievedAt time.Time
	Subjects    []Subject
	TeamHints   []string
	// CategoryHint is a category the source states in structured form
	// (Yahoo status codes); empty when the category comes from the text.
	CategoryHint Category
	// OnlyIfKnown stores the item only as a new version of an article
	// already on record (a Yahoo player with no status is not news unless
	// a status was listed before).
	OnlyIfKnown bool
}

// hashedContent is what makes two versions of an article different. The
// source's update and retrieval times are left out: a re-stamped but
// otherwise identical story is not reprocessed.
type hashedContent struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	// Body is omitted when empty so a version without one hashes as it did
	// before bodies were stored.
	Body         string    `json:"body,omitempty"`
	Author       string    `json:"author"`
	PublishedAt  time.Time `json:"publishedAt"`
	Subjects     []Subject `json:"subjects"`
	TeamHints    []string  `json:"teamHints"`
	CategoryHint Category  `json:"categoryHint"`
}

// ContentHash identifies the item's content.
func (it Item) ContentHash() (string, error) {
	data, err := json.Marshal(hashedContent{
		Title: it.Title, Text: it.Text, Body: it.Body, Author: it.Author, PublishedAt: it.PublishedAt.UTC(),
		Subjects: it.Subjects, TeamHints: it.TeamHints, CategoryHint: it.CategoryHint,
	})
	if err != nil {
		return "", fmt.Errorf("hash item %s: %w", it.ExternalID, err)
	}
	return hashHex(data), nil
}

// TitleFingerprint identifies the title's words regardless of case,
// accents and punctuation; empty when the title is too short to be telling.
func (it Item) TitleFingerprint() string {
	return fingerprint(it.Title)
}

// TextFingerprint is TitleFingerprint for the evidence text.
func (it Item) TextFingerprint() string {
	return fingerprint(it.Text)
}

// ReportedAt is when the source says the story was reported: its
// publication time, else its update time, else when it was retrieved.
func ReportedAt(published, updated, retrieved time.Time) time.Time {
	switch {
	case !published.IsZero():
		return published
	case !updated.IsZero():
		return updated
	default:
		return retrieved
	}
}

func fingerprint(s string) string {
	words := Words(s)
	if len(words) < minFingerprintWords {
		return ""
	}
	return hashHex([]byte(strings.Join(words, " ")))
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Words lowercases s, strips accents and splits it on everything that is
// not a letter or a digit.
func Words(s string) []string {
	return strings.FieldsFunc(matching.NormalizeName(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// CleanText turns feed markup into plain text: tags removed, entities
// decoded, whitespace collapsed, cut to maxRunes at a word boundary.
func CleanText(raw string, maxRunes int) string {
	text := htmlTagPattern.ReplaceAllString(raw, " ")
	text = joinFields(html.UnescapeString(text))
	return truncateRunes(text, maxRunes)
}

// CleanBody turns story markup into plain text that keeps its paragraphs:
// scripts and styles dropped, block tags turned into line breaks, other tags
// removed, entities decoded, blank lines collapsed, cut to maxRunes at a
// word boundary.
func CleanBody(raw string, maxRunes int) string {
	text := htmlCodePattern.ReplaceAllString(raw, " ")
	text = htmlBlockTagPattern.ReplaceAllString(text, "\n")
	text = html.UnescapeString(htmlTagPattern.ReplaceAllString(text, ""))
	var lines []string
	blank := false
	for line := range strings.Lines(text) {
		line = joinFields(line)
		if line == "" {
			blank = len(lines) > 0
			continue
		}
		if blank {
			lines = append(lines, "")
			blank = false
		}
		lines = append(lines, line)
	}
	return truncateRunes(strings.Join(lines, "\n"), maxRunes)
}

// joinFields collapses every run of whitespace in s to one space.
func joinFields(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes cuts text to maxRunes at a word boundary, marking the cut.
func truncateRunes(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)[:maxRunes]
	if cut := strings.LastIndexFunc(string(runes), unicode.IsSpace); cut > 0 {
		return strings.TrimSpace(string(runes)[:cut]) + evidenceEllipsis
	}
	return string(runes) + evidenceEllipsis
}

// normalizeItem cleans an adapter's raw item into what is stored.
func normalizeItem(it Item) Item {
	it.Title = CleanText(it.Title, maxTitleRunes)
	it.Text = CleanText(it.Text, MaxEvidenceRunes)
	it.Body = CleanBody(it.Body, MaxBodyRunes)
	if joinFields(it.Body) == it.Text {
		// A body that only repeats the summary adds nothing.
		it.Body = ""
	}
	it.BodyURL = strings.TrimSpace(it.BodyURL)
	it.Author = CleanText(it.Author, maxTitleRunes)
	it.URL = strings.TrimSpace(it.URL)
	it.ExternalID = strings.TrimSpace(it.ExternalID)
	if it.ExternalID == "" {
		it.ExternalID = it.URL
	}
	hints := make([]string, 0, len(it.TeamHints))
	for _, h := range it.TeamHints {
		if h = strings.ToUpper(strings.TrimSpace(h)); h != "" && !slices.Contains(hints, h) {
			hints = append(hints, h)
		}
	}
	slices.Sort(hints)
	it.TeamHints = hints
	it.Subjects = sortSubjects(it.Subjects)
	return it
}

// sortSubjects returns a sorted copy of subjects: sources may list tags in
// any order, and order must not make a new version.
func sortSubjects(subjects []Subject) []Subject {
	subjects = slices.Clone(subjects)
	slices.SortFunc(subjects, func(a, b Subject) int {
		if a.NHLPlayerID != b.NHLPlayerID {
			return cmp.Compare(a.NHLPlayerID, b.NHLPlayerID)
		}
		if a.YahooPlayerID != b.YahooPlayerID {
			return cmp.Compare(a.YahooPlayerID, b.YahooPlayerID)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return subjects
}
