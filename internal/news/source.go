// Package news ingests attributable player news and structured status
// updates for the draft helper: it fetches configured sources, keeps every
// distinct version of each story, resolves the players a story names against
// the NHL/Yahoo player mapping, and groups repeated or syndicated reports of
// one event into a single incident candidate. It never scores a player.
package news

import (
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Kind says what a source is: an official league/team announcement, a
// structured status feed, or a reporting outlet.
type Kind string

const (
	KindOfficial   Kind = "official"
	KindStructured Kind = "structured"
	KindReporting  Kind = "reporting"
)

// Adapter names how a source is read.
type Adapter string

const (
	// AdapterNHLContent reads the NHL.com content API (stories with NHL
	// player and team tags).
	AdapterNHLContent Adapter = "nhl_content"
	// AdapterRSS reads an RSS 2.0 or Atom feed.
	AdapterRSS Adapter = "rss"
	// AdapterYahooStatus reads the status Yahoo lists in the imported league
	// player pools.
	AdapterYahooStatus Adapter = "yahoo_status"
)

const (
	// defaultStaleRefreshMultiple sets a source's stale threshold to this
	// many refresh intervals when stale_after_minutes is not given.
	defaultStaleRefreshMultiple = 3
	// defaultMaxItems bounds the items read from one fetch when a source does
	// not set max_items.
	defaultMaxItems = 50
	// ScopeFeed is the fetch-state scope of a source that is one feed.
	ScopeFeed = "feed"
	// scopeSeasonFormat is the fetch-state scope of a per-season source.
	scopeSeasonFormat = "season:%d"
)

// ErrInvalidSources means the sources configuration cannot be used.
var ErrInvalidSources = errors.New("invalid news sources")

//go:embed sources.yaml
var defaultSourcesYAML []byte

// Source is one configured news source.
type Source struct {
	ID                string  `yaml:"id" json:"id"`
	Publisher         string  `yaml:"publisher" json:"publisher"`
	Kind              Kind    `yaml:"kind" json:"kind"`
	Adapter           Adapter `yaml:"adapter" json:"adapter"`
	URL               string  `yaml:"url" json:"url,omitempty"`
	Priority          int     `yaml:"priority" json:"priority"`
	RefreshMinutes    int     `yaml:"refresh_minutes" json:"refreshMinutes"`
	StaleAfterMinutes int     `yaml:"stale_after_minutes" json:"staleAfterMinutes,omitempty"`
	MaxItems          int     `yaml:"max_items" json:"maxItems,omitempty"`
	// FetchBody downloads each new or updated story's page for its full
	// text (NHL.com content API sources only).
	FetchBody bool   `yaml:"fetch_body" json:"fetchBody,omitempty"`
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Notes     string `yaml:"notes" json:"notes,omitempty"`
}

type sourcesFile struct {
	Sources []Source `yaml:"sources"`
}

// RefreshInterval is the minimum time between two fetches of the source.
func (s Source) RefreshInterval() time.Duration {
	return time.Duration(s.RefreshMinutes) * time.Minute
}

// StaleAfter is the age after which the source's coverage is stale.
func (s Source) StaleAfter() time.Duration {
	if s.StaleAfterMinutes > 0 {
		return time.Duration(s.StaleAfterMinutes) * time.Minute
	}
	return defaultStaleRefreshMultiple * s.RefreshInterval()
}

// ItemLimit is the number of items read from one fetch.
func (s Source) ItemLimit() int {
	if s.MaxItems > 0 {
		return s.MaxItems
	}
	return defaultMaxItems
}

// Scope is the fetch-state scope of the source: its feed, or for the Yahoo
// status source the season whose pools it reads.
func (s Source) Scope(season int) string {
	if s.Adapter == AdapterYahooStatus {
		return fmt.Sprintf(scopeSeasonFormat, season)
	}
	return ScopeFeed
}

// Validate reports the first problem with the source definition.
func (s Source) Validate() error {
	switch {
	case s.ID == "":
		return fmt.Errorf("%w: a source has no id", ErrInvalidSources)
	case s.Publisher == "":
		return fmt.Errorf("%w: source %s has no publisher", ErrInvalidSources, s.ID)
	case !slices.Contains([]Kind{KindOfficial, KindStructured, KindReporting}, s.Kind):
		return fmt.Errorf("%w: source %s has unknown kind %q", ErrInvalidSources, s.ID, s.Kind)
	case s.RefreshMinutes <= 0:
		return fmt.Errorf("%w: source %s needs a positive refresh_minutes", ErrInvalidSources, s.ID)
	case s.StaleAfterMinutes < 0 || s.MaxItems < 0:
		return fmt.Errorf("%w: source %s has a negative limit", ErrInvalidSources, s.ID)
	case s.FetchBody && s.Adapter != AdapterNHLContent:
		return fmt.Errorf("%w: source %s: fetch_body is only supported by the %s adapter", ErrInvalidSources, s.ID, AdapterNHLContent)
	}
	switch s.Adapter {
	case AdapterYahooStatus:
		return nil
	case AdapterNHLContent, AdapterRSS:
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%w: source %s needs an http(s) url, got %q", ErrInvalidSources, s.ID, s.URL)
		}
		return nil
	default:
		return fmt.Errorf("%w: source %s has unknown adapter %q", ErrInvalidSources, s.ID, s.Adapter)
	}
}

// ParseSources reads a sources file and validates every source; IDs must be
// unique.
func ParseSources(data []byte) ([]Source, error) {
	var file sourcesFile
	if err := yaml.UnmarshalStrict(data, &file); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSources, err)
	}
	seen := make(map[string]bool, len(file.Sources))
	for _, s := range file.Sources {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("%w: duplicate source id %s", ErrInvalidSources, s.ID)
		}
		seen[s.ID] = true
	}
	return file.Sources, nil
}

// DefaultSources returns the built-in source set.
func DefaultSources() ([]Source, error) {
	return ParseSources(defaultSourcesYAML)
}

// LoadSources reads the sources file at path, or the built-in set when path
// is empty.
func LoadSources(path string) ([]Source, error) {
	if path == "" {
		return DefaultSources()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read news sources %s: %w", path, err)
	}
	return ParseSources(data)
}

// EnabledSources returns the enabled sources, most authoritative first. When
// ids is not empty only those sources are kept; an unknown or disabled id is
// an error so a typo cannot silently skip a source.
func EnabledSources(sources []Source, ids []string) ([]Source, error) {
	byID := make(map[string]Source, len(sources))
	for _, s := range sources {
		byID[s.ID] = s
	}
	var out []Source
	if len(ids) == 0 {
		for _, s := range sources {
			if s.Enabled {
				out = append(out, s)
			}
		}
	} else {
		for _, id := range ids {
			s, ok := byID[strings.TrimSpace(id)]
			if !ok || !s.Enabled {
				return nil, fmt.Errorf("%w: %q is not an enabled source", ErrInvalidSources, id)
			}
			out = append(out, s)
		}
	}
	SortByPriority(out)
	return out, nil
}

// SortByPriority orders sources by priority, then ID.
func SortByPriority(sources []Source) {
	slices.SortStableFunc(sources, func(a, b Source) int {
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		return strings.Compare(a.ID, b.ID)
	})
}
