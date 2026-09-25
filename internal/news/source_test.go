package news

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

// enabledDefaultSourceIDs are the sources DefaultSources must enable.
var enabledDefaultSourceIDs = []string{
	"nhl-player-safety", "nhl-injury", "nhl-transactions", "nhl-press-releases",
	"yahoo-status", "rotowire-nhl", "sportsnet-nhl",
}

func TestDefaultSourcesParseAndValidate(t *testing.T) {
	sources, err := DefaultSources()
	require.NoError(t, err)
	require.NotEmpty(t, sources)
	for _, s := range sources {
		assert.NoError(t, s.Validate(), "source %s", s.ID)
	}
}

func TestDefaultSourcesEnabledSet(t *testing.T) {
	sources, err := DefaultSources()
	require.NoError(t, err)
	enabled := map[string]bool{}
	for _, s := range sources {
		enabled[s.ID] = s.Enabled
	}
	for _, id := range enabledDefaultSourceIDs {
		assert.True(t, enabled[id], "%s should be enabled by default", id)
	}
	assert.False(t, enabled["espn-nhl"], "espn-nhl is disabled by default")
}

// baseSource is a source definition that passes Validate unmodified; tests
// mutate a copy to exercise one rejection at a time.
func baseSource() Source {
	return Source{
		ID: "a", Publisher: "A", Kind: KindOfficial, Adapter: AdapterRSS, URL: "https://a.example.com/feed",
		Priority: 1, RefreshMinutes: 30, Enabled: true,
	}
}

// marshalSources renders sources as a sources.yaml file using Source's own
// yaml tags, so the fixture cannot drift from the struct it exercises.
func marshalSources(t *testing.T, sources ...Source) []byte {
	t.Helper()
	data, err := yaml.Marshal(sourcesFile{Sources: sources})
	require.NoError(t, err)
	return data
}

func TestParseSourcesRejectsUnknownField(t *testing.T) {
	data := string(marshalSources(t, baseSource())) + "bogus_top_level_field: true\n"
	_, err := ParseSources([]byte(data))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestParseSourcesRejectsDuplicateIDs(t *testing.T) {
	a, b := baseSource(), baseSource()
	b.Publisher = "B"
	b.URL = "https://b.example.com/feed"
	_, err := ParseSources(marshalSources(t, a, b))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
	assert.Contains(t, err.Error(), "duplicate source id")
}

func TestParseSourcesRejectsUnknownKind(t *testing.T) {
	s := baseSource()
	s.Kind = "bogus"
	_, err := ParseSources(marshalSources(t, s))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestParseSourcesRejectsUnknownAdapter(t *testing.T) {
	s := baseSource()
	s.Adapter = "bogus"
	_, err := ParseSources(marshalSources(t, s))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestParseSourcesRejectsNonHTTPURL(t *testing.T) {
	for _, adapter := range []Adapter{AdapterRSS, AdapterNHLContent} {
		s := baseSource()
		s.Adapter = adapter
		s.URL = "ftp://example.com/feed"
		_, err := ParseSources(marshalSources(t, s))
		require.Error(t, err, "adapter %s", adapter)
		assert.ErrorIs(t, err, ErrInvalidSources)
	}
}

func TestParseSourcesRejectsNonPositiveRefreshMinutes(t *testing.T) {
	for _, minutes := range []int{0, -1} {
		s := baseSource()
		s.RefreshMinutes = minutes
		_, err := ParseSources(marshalSources(t, s))
		require.Error(t, err, "refresh_minutes %d", minutes)
		assert.ErrorIs(t, err, ErrInvalidSources)
	}
}

func TestParseSourcesRejectsNegativeLimits(t *testing.T) {
	stale := baseSource()
	stale.StaleAfterMinutes = -1
	_, err := ParseSources(marshalSources(t, stale))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)

	maxItems := baseSource()
	maxItems.MaxItems = -1
	_, err = ParseSources(marshalSources(t, maxItems))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestYahooStatusSourceNeedsNoURL(t *testing.T) {
	s := Source{ID: "y", Publisher: "Yahoo", Kind: KindStructured, Adapter: AdapterYahooStatus, RefreshMinutes: 60}
	assert.NoError(t, s.Validate())
}

func TestEnabledSourcesWithNilIDsSortsByPriorityThenID(t *testing.T) {
	sources := []Source{
		{ID: "z", Publisher: "Z", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 5, Enabled: true},
		{ID: "b", Publisher: "B", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 1, Enabled: true},
		{ID: "a", Publisher: "A", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 1, Enabled: true},
		{ID: "disabled", Publisher: "D", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 0, Enabled: false},
	}
	out, err := EnabledSources(sources, nil)
	require.NoError(t, err)
	var ids []string
	for _, s := range out {
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{"a", "b", "z"}, ids)
}

func TestEnabledSourcesWithIDsReturnsThemInPriorityOrder(t *testing.T) {
	sources := []Source{
		{ID: "low-priority", Publisher: "L", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 90, Enabled: true},
		{ID: "high-priority", Publisher: "H", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Priority: 10, Enabled: true},
	}
	// Requested out of priority order; the result must still come back sorted.
	out, err := EnabledSources(sources, []string{"low-priority", "high-priority"})
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "high-priority", out[0].ID)
	assert.Equal(t, "low-priority", out[1].ID)
}

func TestEnabledSourcesRejectsUnknownID(t *testing.T) {
	sources := []Source{{ID: "a", Publisher: "A", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Enabled: true}}
	_, err := EnabledSources(sources, []string{"missing"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestEnabledSourcesRejectsDisabledID(t *testing.T) {
	sources := []Source{{ID: "a", Publisher: "A", Kind: KindReporting, Adapter: AdapterYahooStatus, RefreshMinutes: 60, Enabled: false}}
	_, err := EnabledSources(sources, []string{"a"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSources)
}

func TestStaleAfterDefaultsToThreeTimesRefresh(t *testing.T) {
	s := Source{RefreshMinutes: 10}
	assert.Equal(t, 30*time.Minute, s.StaleAfter())
}

func TestStaleAfterHonorsStaleAfterMinutes(t *testing.T) {
	s := Source{RefreshMinutes: 10, StaleAfterMinutes: 5}
	assert.Equal(t, 5*time.Minute, s.StaleAfter())
}

func TestItemLimitDefault(t *testing.T) {
	assert.Equal(t, defaultMaxItems, Source{}.ItemLimit())
	assert.Equal(t, 7, Source{MaxItems: 7}.ItemLimit())
}

const testSeason = 2026

func TestScope(t *testing.T) {
	assert.Equal(t, ScopeFeed, Source{Adapter: AdapterRSS}.Scope(testSeason))
	assert.Equal(t, ScopeFeed, Source{Adapter: AdapterNHLContent}.Scope(testSeason))
	assert.Equal(t, "season:2026", Source{Adapter: AdapterYahooStatus}.Scope(testSeason))
}

func TestLoadSourcesEmptyPathEqualsDefaults(t *testing.T) {
	want, err := DefaultSources()
	require.NoError(t, err)
	got, err := LoadSources("")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLoadSourcesReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.yaml")
	require.NoError(t, os.WriteFile(path, marshalSources(t, baseSource()), 0o600))
	got, err := LoadSources(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].ID)
}

func TestLoadSourcesReportsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, err := LoadSources(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

func TestParseSourcesRejectsFetchBodyOutsideNHLContent(t *testing.T) {
	for _, adapter := range []Adapter{AdapterRSS, AdapterYahooStatus} {
		s := baseSource()
		s.Adapter = adapter
		s.FetchBody = true
		_, err := ParseSources(marshalSources(t, s))
		require.Error(t, err, "adapter %s", adapter)
		assert.ErrorIs(t, err, ErrInvalidSources)
	}
	s := baseSource()
	s.Adapter, s.FetchBody = AdapterNHLContent, true
	_, err := ParseSources(marshalSources(t, s))
	assert.NoError(t, err)
}

func TestDefaultSourcesFetchNHLStoryBodies(t *testing.T) {
	sources, err := DefaultSources()
	require.NoError(t, err)
	for _, s := range sources {
		assert.Equal(t, s.Adapter == AdapterNHLContent, s.FetchBody, "source %s", s.ID)
	}
}
