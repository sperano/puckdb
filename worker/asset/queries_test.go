package asset

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/require"
)

// ─── Mock assetQueries ────────────────────────────────────────────────────────

// mockAssetQueries is a manual mock that implements assetQueries for testing.
// Each List field is the canned return value for the corresponding method.
// Count methods return len(<list field>) so tests can stage rows once and
// have both list and count behave consistently.
type mockAssetQueries struct {
	headshots     []sqlcdb.ListPlayerHeadshotsRow
	heroImages    []sqlcdb.ListPlayerHeroImagesRow
	yahooImages   []sqlcdb.ListPlayerYahooImagesRow
	teamLogos     []sqlcdb.ListTeamLogosRow
	yahooTeamLogs []sqlcdb.ListYahooTeamLogosRow
	leagueLogos   []sqlcdb.ListYahooLeagueLogosRow
	managerImages []sqlcdb.ListYahooManagerImagesRow
}

func (m *mockAssetQueries) ListPlayerHeadshots(_ context.Context) ([]sqlcdb.ListPlayerHeadshotsRow, error) {
	return m.headshots, nil
}
func (m *mockAssetQueries) ListPlayerHeroImages(_ context.Context) ([]sqlcdb.ListPlayerHeroImagesRow, error) {
	return m.heroImages, nil
}
func (m *mockAssetQueries) ListPlayerYahooImages(_ context.Context) ([]sqlcdb.ListPlayerYahooImagesRow, error) {
	return m.yahooImages, nil
}
func (m *mockAssetQueries) ListTeamLogos(_ context.Context) ([]sqlcdb.ListTeamLogosRow, error) {
	return m.teamLogos, nil
}
func (m *mockAssetQueries) ListYahooTeamLogos(_ context.Context) ([]sqlcdb.ListYahooTeamLogosRow, error) {
	return m.yahooTeamLogs, nil
}
func (m *mockAssetQueries) ListYahooLeagueLogos(_ context.Context) ([]sqlcdb.ListYahooLeagueLogosRow, error) {
	return m.leagueLogos, nil
}
func (m *mockAssetQueries) ListYahooManagerImages(_ context.Context) ([]sqlcdb.ListYahooManagerImagesRow, error) {
	return m.managerImages, nil
}

func (m *mockAssetQueries) CountPlayerHeadshots(_ context.Context) (int64, error) {
	return int64(len(m.headshots)), nil
}
func (m *mockAssetQueries) CountPlayerHeroImages(_ context.Context) (int64, error) {
	return int64(len(m.heroImages)), nil
}
func (m *mockAssetQueries) CountPlayerYahooImages(_ context.Context) (int64, error) {
	return int64(len(m.yahooImages)), nil
}
func (m *mockAssetQueries) CountTeamLogos(_ context.Context) (int64, error) {
	return int64(len(m.teamLogos)), nil
}
func (m *mockAssetQueries) CountYahooTeamLogos(_ context.Context) (int64, error) {
	return int64(len(m.yahooTeamLogs)), nil
}
func (m *mockAssetQueries) CountYahooLeagueLogos(_ context.Context) (int64, error) {
	return int64(len(m.leagueLogos)), nil
}
func (m *mockAssetQueries) CountYahooManagerImages(_ context.Context) (int64, error) {
	return int64(len(m.managerImages)), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newTestActivities(q *mockAssetQueries) *Activities {
	return &Activities{
		Storage:  store.NewMemStorage(),
		Download: nil, // queries_test only exercises the Queries path
		Queries:  q,
	}
}

func validText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestLoadPlayerHeadshotAssets(t *testing.T) {
	t.Parallel()
	q := &mockAssetQueries{
		headshots: []sqlcdb.ListPlayerHeadshotsRow{
			{ID: 8478402, HeadshotURL: "https://assets.nhle.com/mugs/nhl/20242025/8478402.png"},
			{ID: 8471214, HeadshotURL: "https://assets.nhle.com/mugs/nhl/20242025/8471214.png"},
		},
	}
	a := newTestActivities(q)
	assets, err := a.LoadPlayerHeadshotAssets(context.Background())
	require.NoError(t, err)
	require.Len(t, assets, 2)

	require.Equal(t, core.PlayerHeadshot, assets[0].FileType)
	require.Equal(t, []int64{8478402}, assets[0].IDs)
	require.Equal(t, "https://assets.nhle.com/mugs/nhl/20242025/8478402.png", assets[0].URL)

	require.Equal(t, core.PlayerHeadshot, assets[1].FileType)
	require.Equal(t, []int64{8471214}, assets[1].IDs)
}

func TestLoadPlayerHeadshotAssets_Empty(t *testing.T) {
	t.Parallel()
	a := newTestActivities(&mockAssetQueries{})
	assets, err := a.LoadPlayerHeadshotAssets(context.Background())
	require.NoError(t, err)
	require.Empty(t, assets)
}

func TestLoadPlayerHeroImageAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rows      []sqlcdb.ListPlayerHeroImagesRow
		wantCount int
		wantURL   string
	}{
		{
			name: "valid nullable text",
			rows: []sqlcdb.ListPlayerHeroImagesRow{
				{ID: 8478402, HeroImageURL: validText("https://assets.nhle.com/hero/8478402.jpg")},
			},
			wantCount: 1,
			wantURL:   "https://assets.nhle.com/hero/8478402.jpg",
		},
		{
			name: "null url skipped defensively",
			rows: []sqlcdb.ListPlayerHeroImagesRow{
				{ID: 8478402, HeroImageURL: pgtype.Text{Valid: false}},
			},
			wantCount: 0,
		},
		{
			name: "empty string skipped defensively",
			rows: []sqlcdb.ListPlayerHeroImagesRow{
				{ID: 8478402, HeroImageURL: pgtype.Text{String: "", Valid: true}},
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newTestActivities(&mockAssetQueries{heroImages: tc.rows})
			assets, err := a.LoadPlayerHeroImageAssets(context.Background())
			require.NoError(t, err)
			require.Len(t, assets, tc.wantCount)
			if tc.wantCount > 0 {
				require.Equal(t, core.PlayerHeroImage, assets[0].FileType)
				require.Equal(t, tc.wantURL, assets[0].URL)
			}
		})
	}
}

func TestLoadPlayerYahooImageAssets(t *testing.T) {
	t.Parallel()
	q := &mockAssetQueries{
		yahooImages: []sqlcdb.ListPlayerYahooImagesRow{
			{ID: 8478402, YahooImage: "https://s.yimg.com/iu/api/res/1.2/player8478402.jpg"},
		},
	}
	a := newTestActivities(q)
	assets, err := a.LoadPlayerYahooImageAssets(context.Background())
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Equal(t, core.PlayerYahooImage, assets[0].FileType)
	require.Equal(t, []int64{8478402}, assets[0].IDs)
}

func TestLoadTeamLogoAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rows      []sqlcdb.ListTeamLogosRow
		wantCount int
	}{
		{
			name: "valid logo",
			rows: []sqlcdb.ListTeamLogosRow{
				{TeamID: 10, LogoUrl: validText("https://assets.nhle.com/logos/nhl/svg/TOR_light.svg")},
			},
			wantCount: 1,
		},
		{
			name: "null logo_url skipped defensively",
			rows: []sqlcdb.ListTeamLogosRow{
				{TeamID: 10, LogoUrl: pgtype.Text{Valid: false}},
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newTestActivities(&mockAssetQueries{teamLogos: tc.rows})
			assets, err := a.LoadTeamLogoAssets(context.Background())
			require.NoError(t, err)
			require.Len(t, assets, tc.wantCount)
			if tc.wantCount > 0 {
				require.Equal(t, core.TeamLogo, assets[0].FileType)
				require.Equal(t, []int64{10}, assets[0].IDs)
			}
		})
	}
}

func TestLoadYahooTeamLogoAssets(t *testing.T) {
	t.Parallel()
	q := &mockAssetQueries{
		yahooTeamLogs: []sqlcdb.ListYahooTeamLogosRow{
			{LeagueID: 100, ID: 1, LogoUrl: "https://s.yimg.com/lj/asset/img/nhl-teams/logos/team1.png"},
		},
	}
	a := newTestActivities(q)
	assets, err := a.LoadYahooTeamLogoAssets(context.Background())
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Equal(t, core.YahooTeamLogo, assets[0].FileType)
	require.Equal(t, []int64{100, 1}, assets[0].IDs)
}

func TestLoadYahooLeagueLogoAssets(t *testing.T) {
	t.Parallel()
	q := &mockAssetQueries{
		leagueLogos: []sqlcdb.ListYahooLeagueLogosRow{
			{ID: 42, LogoUrl: "https://s.yimg.com/lj/asset/img/nhl-leagues/logos/42.png"},
		},
	}
	a := newTestActivities(q)
	assets, err := a.LoadYahooLeagueLogoAssets(context.Background())
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Equal(t, core.YahooLeagueLogo, assets[0].FileType)
	require.Equal(t, []int64{42}, assets[0].IDs)
}

func TestLoadYahooManagerImageAssets(t *testing.T) {
	t.Parallel()
	q := &mockAssetQueries{
		managerImages: []sqlcdb.ListYahooManagerImagesRow{
			{LeagueID: 100, TeamID: 3, ID: 7, ImageUrl: "https://s.yimg.com/lj/asset/img/user/7.jpg"},
		},
	}
	a := newTestActivities(q)
	assets, err := a.LoadYahooManagerImageAssets(context.Background())
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Equal(t, core.YahooManagerImage, assets[0].FileType)
	require.Equal(t, []int64{100, 3, 7}, assets[0].IDs)
}

// TestCountActivities verifies each count activity returns the row count from
// the underlying sqlc query, downcast from int64 to int. The mock counts each
// table by len() of the staged list, so a count value should match len(rows).
func TestCountActivities(t *testing.T) {
	t.Parallel()

	q := &mockAssetQueries{
		headshots:     make([]sqlcdb.ListPlayerHeadshotsRow, 3),
		heroImages:    make([]sqlcdb.ListPlayerHeroImagesRow, 5),
		yahooImages:   make([]sqlcdb.ListPlayerYahooImagesRow, 13),
		teamLogos:     make([]sqlcdb.ListTeamLogosRow, 17),
		yahooTeamLogs: make([]sqlcdb.ListYahooTeamLogosRow, 19),
		leagueLogos:   make([]sqlcdb.ListYahooLeagueLogosRow, 23),
		managerImages: make([]sqlcdb.ListYahooManagerImagesRow, 29),
	}
	a := newTestActivities(q)
	ctx := context.Background()

	type counter func(context.Context) (int, error)
	cases := []struct {
		name string
		fn   counter
		want int
	}{
		{"PlayerHeadshot", a.CountPlayerHeadshotAssets, 3},
		{"PlayerHeroImage", a.CountPlayerHeroImageAssets, 5},
		{"PlayerYahooImage", a.CountPlayerYahooImageAssets, 13},
		{"TeamLogo", a.CountTeamLogoAssets, 17},
		{"YahooTeamLogo", a.CountYahooTeamLogoAssets, 19},
		{"YahooLeagueLogo", a.CountYahooLeagueLogoAssets, 23},
		{"YahooManagerImage", a.CountYahooManagerImageAssets, 29},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.fn(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
