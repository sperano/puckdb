package newsevent

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"gopkg.in/yaml.v3"
)

// builtinCorpus is the labeled evaluation corpus (see evalcorpus.yaml).
//
//go:embed evalcorpus.yaml
var builtinCorpus []byte

// Corpus is a labeled evaluation set: synthetic articles, in the order they
// are published, with the events and lifecycle each must produce.
type Corpus struct {
	Version string `yaml:"version"`
	Cases   []Case `yaml:"cases"`
}

// Case is one scenario: a sequence of articles about the same players.
type Case struct {
	ID string `yaml:"id"`
	// Covers names the acceptance scenario the case exercises.
	Covers      string `yaml:"covers"`
	Description string `yaml:"description"`
	// Malicious marks an article carrying instructions for the model.
	Malicious bool         `yaml:"malicious"`
	Players   []CasePlayer `yaml:"players"`
	Steps     []Step       `yaml:"steps"`
	// Final is the events on record after the last step.
	Final []Expected `yaml:"final"`
}

// CasePlayer is a player offered to the model in every step of a case.
type CasePlayer struct {
	Ref   string `yaml:"ref"`
	Name  string `yaml:"name"`
	NHLID int64  `yaml:"nhl_id"`
	Team  string `yaml:"team"`
}

// Step is one article version.
type Step struct {
	// Article keys the article: a key seen before makes this a newer
	// version of that article.
	Article   string    `yaml:"article"`
	Publisher string    `yaml:"publisher"`
	Kind      news.Kind `yaml:"kind"`
	Published time.Time `yaml:"published"`
	Title     string    `yaml:"title"`
	Text      string    `yaml:"text"`
	// Reply is a reference reply: what a correct model answers. Offline
	// tests replay it; a live evaluation ignores it.
	Reply  string     `yaml:"reply"`
	Expect []Expected `yaml:"expect"`
	// Review is set when the step must be routed to review.
	Review bool `yaml:"review"`
}

// Expected is a labeled event.
type Expected struct {
	Player    string       `yaml:"player"`
	Type      Type         `yaml:"type"`
	Status    ReportStatus `yaml:"status"`
	Duration  DurationKind `yaml:"duration"`
	Games     int          `yaml:"games"`
	Days      int          `yaml:"days"`
	Until     string       `yaml:"until"`
	Effective string       `yaml:"effective"`
	ChangeTo  string       `yaml:"change_to"`
	// Lifecycle is checked in Case.Final only.
	Lifecycle Lifecycle `yaml:"lifecycle"`
}

// LoadCorpus reads a corpus file, or the built-in corpus when path is empty.
func LoadCorpus(path string) (Corpus, error) {
	data := builtinCorpus
	if path != "" {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return Corpus{}, fmt.Errorf("read evaluation corpus: %w", err)
		}
	}
	var c Corpus
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Corpus{}, fmt.Errorf("parse evaluation corpus: %w", err)
	}
	if err := c.validate(); err != nil {
		return Corpus{}, err
	}
	return c, nil
}

func (c Corpus) validate() error {
	if c.Version == "" || len(c.Cases) == 0 {
		return fmt.Errorf("evaluation corpus needs a version and cases")
	}
	seen := make(map[string]bool)
	for _, tc := range c.Cases {
		if tc.ID == "" || seen[tc.ID] || len(tc.Steps) == 0 || len(tc.Players) == 0 {
			return fmt.Errorf("evaluation case %q needs a unique id, players and steps", tc.ID)
		}
		seen[tc.ID] = true
		for i, s := range tc.Steps {
			if s.Article == "" || s.Published.IsZero() || s.Text == "" {
				return fmt.Errorf("evaluation case %s step %d needs an article, a publication time and text", tc.ID, i+1)
			}
		}
	}
	return nil
}

// identity is the player a case ref stands for.
func (p CasePlayer) identity() news.Identity {
	return news.Identity{NHLPlayerID: p.NHLID, Name: p.Name, Team: p.Team}
}

// caseRun replays one case's articles with stable version and article IDs.
type caseRun struct {
	tc       Case
	articles map[string]int64
	versions map[string]int
	nextID   int64
}

func newCaseRun(tc Case) *caseRun {
	return &caseRun{tc: tc, articles: make(map[string]int64), versions: make(map[string]int)}
}

// step returns the input and report of the case's i-th article version.
func (cr *caseRun) step(i, maxRunes int) (Input, Report) {
	s := cr.tc.Steps[i]
	if _, ok := cr.articles[s.Article]; !ok {
		cr.articles[s.Article] = int64(len(cr.articles) + 1)
	}
	cr.versions[s.Article]++
	cr.nextID++
	r := Report{
		VersionID: cr.nextID, ArticleID: cr.articles[s.Article], Version: cr.versions[s.Article],
		Publisher: s.Publisher, Kind: s.Kind, ReportedAt: s.Published.UTC(),
	}
	in := Input{Documents: []Document{{
		Ref: firstDocumentRef, VersionID: r.VersionID, ArticleID: r.ArticleID, Version: r.Version,
		Publisher: s.Publisher, Kind: s.Kind, Title: s.Title, Text: DocumentText(s.Text, "", maxRunes), ReportedAt: r.ReportedAt,
	}}}
	for _, p := range cr.tc.Players {
		in.Players = append(in.Players, Player{Ref: p.Ref, Identity: p.identity()})
	}
	return in, r
}

// player returns the identity of a case ref.
func (cr *caseRun) player(ref string) news.Identity {
	for _, p := range cr.tc.Players {
		if p.Ref == ref {
			return p.identity()
		}
	}
	return news.Identity{Name: ref}
}

// matches reports whether an event states exactly the labeled facts.
func (cr *caseRun) matches(want Expected, got Event) bool {
	return cr.sameEvent(want, got) && fieldsMatch(want, got)
}

// sameEvent reports whether an event is the labeled one: same player, type
// and report status.
func (cr *caseRun) sameEvent(want Expected, got Event) bool {
	return samePlayer(cr.player(want.Player), got.Player) && want.Type == got.Type && want.Status == got.Status
}

func fieldsMatch(want Expected, got Event) bool {
	kind := want.Duration
	if kind == "" {
		kind = DurationUnknown
	}
	return kind == got.Duration.Kind && want.Games == got.Duration.Games && want.Days == got.Duration.Days &&
		want.Until == formatDate(got.Duration.Until) && want.Effective == formatDate(got.EffectiveOn) &&
		sameText(want.ChangeTo, got.Change.To)
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}

// describe renders a labeled event for failure messages.
func (e Expected) describe() string {
	s := e.Player + " " + string(e.Type) + " " + string(e.Status) + " " + string(e.Duration)
	if e.Games != 0 {
		s += " " + strconv.Itoa(e.Games) + " games"
	}
	if e.ChangeTo != "" {
		s += " to " + e.ChangeTo
	}
	return s
}
