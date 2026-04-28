package asset

import (
	"strings"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/stretchr/testify/require"
)

func TestAssetPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		asset   Asset
		want    string
		wantErr bool
	}{
		// PlayerHeadshot
		{
			name:  "PlayerHeadshot happy path",
			asset: Asset{FileType: core.PlayerHeadshot, URL: "https://assets.nhle.com/mugs/actionshots/1296x729/8478402.jpg", IDs: []int64{8478402}},
			want:  "assets/players/8478402/headshot.jpg",
		},
		// PlayerHeroImage
		{
			name:  "PlayerHeroImage happy path",
			asset: Asset{FileType: core.PlayerHeroImage, URL: "https://assets.nhle.com/mugs/hero/8478402.png", IDs: []int64{8478402}},
			want:  "assets/players/8478402/hero.png",
		},
		// PlayerYahooImageSmall
		{
			name:  "PlayerYahooImageSmall happy path",
			asset: Asset{FileType: core.PlayerYahooImageSmall, URL: "https://s.yimg.com/iu/api/res/1.2/small_8478402.jpg", IDs: []int64{8478402}},
			want:  "assets/players/8478402/yahoo-small.jpg",
		},
		// PlayerYahooImageMedium
		{
			name:  "PlayerYahooImageMedium happy path",
			asset: Asset{FileType: core.PlayerYahooImageMedium, URL: "https://s.yimg.com/iu/api/res/1.2/med_8478402.jpg", IDs: []int64{8478402}},
			want:  "assets/players/8478402/yahoo-medium.jpg",
		},
		// PlayerYahooImageLarge
		{
			name:  "PlayerYahooImageLarge happy path",
			asset: Asset{FileType: core.PlayerYahooImageLarge, URL: "https://s.yimg.com/iu/api/res/1.2/large_8478402.jpg", IDs: []int64{8478402}},
			want:  "assets/players/8478402/yahoo-large.jpg",
		},
		// TeamLogo
		{
			name:  "TeamLogo happy path",
			asset: Asset{FileType: core.TeamLogo, URL: "https://assets.nhle.com/logos/nhl/svg/MTL_light.svg", IDs: []int64{8}},
			want:  "assets/teams/logos/8.svg",
		},
		// YahooTeamLogo
		{
			name:  "YahooTeamLogo happy path",
			asset: Asset{FileType: core.YahooTeamLogo, URL: "https://s.yimg.com/fantasy/teams/logo/42_7.png", IDs: []int64{1234, 7}},
			want:  "assets/yahoo/leagues/1234/teams/7/logo.png",
		},
		// YahooLeagueLogo
		{
			name:  "YahooLeagueLogo happy path",
			asset: Asset{FileType: core.YahooLeagueLogo, URL: "https://s.yimg.com/fantasy/leagues/logo/1234.png", IDs: []int64{1234}},
			want:  "assets/yahoo/leagues/1234/league-logo.png",
		},
		// YahooManagerImage
		{
			name:  "YahooManagerImage happy path",
			asset: Asset{FileType: core.YahooManagerImage, URL: "https://s.yimg.com/fantasy/managers/pic/555.jpg", IDs: []int64{1234, 7, 555}},
			want:  "assets/yahoo/leagues/1234/teams/7/manager-555.jpg",
		},

		// Arity mismatch errors
		{
			name:    "PlayerHeadshot no IDs",
			asset:   Asset{FileType: core.PlayerHeadshot, URL: "https://assets.nhle.com/mugs/8478402.jpg", IDs: []int64{}},
			wantErr: true,
		},
		{
			name:    "YahooManagerImage too few IDs",
			asset:   Asset{FileType: core.YahooManagerImage, URL: "https://s.yimg.com/managers/555.jpg", IDs: []int64{1234, 7}},
			wantErr: true,
		},
		{
			name:    "YahooTeamLogo too many IDs",
			asset:   Asset{FileType: core.YahooTeamLogo, URL: "https://s.yimg.com/teams/logo.png", IDs: []int64{1, 2, 3}},
			wantErr: true,
		},

		// Bad URL extension propagates through Path()
		{
			name:    "unsupported extension propagated",
			asset:   Asset{FileType: core.PlayerHeadshot, URL: "https://assets.nhle.com/mugs/8478402.aspx", IDs: []int64{8478402}},
			wantErr: true,
		},

		// Unknown FileType
		{
			name:    "unknown FileType no builder",
			asset:   Asset{FileType: core.Unknown, URL: "https://example.com/foo.png", IDs: []int64{}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.asset.Path()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAssetPath_ExtErrorContainsFileTypeName verifies that when extFromURL
// fails, the error message includes the FileType name so callers can identify
// which asset class caused the problem.
func TestAssetPath_ExtErrorContainsFileTypeName(t *testing.T) {
	t.Parallel()
	a := Asset{
		FileType: core.PlayerHeroImage,
		URL:      "https://assets.nhle.com/mugs/8478402.html",
		IDs:      []int64{8478402},
	}
	_, err := a.Path()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "PlayerHeroImage"),
		"error %q should mention the FileType name", err.Error())
}

// TestConstructors verifies that each typed constructor sets the correct
// FileType, IDs in the right order, and URL.
func TestConstructors(t *testing.T) {
	t.Parallel()

	const (
		testURL       = "https://example.com/image.png"
		testPlayerID  = nhl.PlayerID(8478402)
		testTeamID    = int64(8)
		testLeagueID  = int32(1234)
		testYahooTeam = int32(7)
		testManagerID = int32(555)
	)

	t.Run("NewPlayerHeadshot", func(t *testing.T) {
		t.Parallel()
		a := NewPlayerHeadshot(testPlayerID, testURL)
		require.Equal(t, core.PlayerHeadshot, a.FileType)
		require.Equal(t, []int64{int64(testPlayerID)}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})

	t.Run("NewPlayerHeroImage", func(t *testing.T) {
		t.Parallel()
		a := NewPlayerHeroImage(testPlayerID, testURL)
		require.Equal(t, core.PlayerHeroImage, a.FileType)
		require.Equal(t, []int64{int64(testPlayerID)}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})

	t.Run("NewPlayerYahooImage_Small", func(t *testing.T) {
		t.Parallel()
		a := NewPlayerYahooImage(testPlayerID, YahooImageSizeSmall, testURL)
		require.Equal(t, core.PlayerYahooImageSmall, a.FileType)
		require.Equal(t, []int64{int64(testPlayerID)}, a.IDs)
	})

	t.Run("NewPlayerYahooImage_Medium", func(t *testing.T) {
		t.Parallel()
		a := NewPlayerYahooImage(testPlayerID, YahooImageSizeMedium, testURL)
		require.Equal(t, core.PlayerYahooImageMedium, a.FileType)
	})

	t.Run("NewPlayerYahooImage_Large", func(t *testing.T) {
		t.Parallel()
		a := NewPlayerYahooImage(testPlayerID, YahooImageSizeLarge, testURL)
		require.Equal(t, core.PlayerYahooImageLarge, a.FileType)
	})

	t.Run("NewPlayerYahooImage_invalidSize_surfacesAsPathError", func(t *testing.T) {
		t.Parallel()
		const invalidSize = YahooImageSize(99)
		a := NewPlayerYahooImage(testPlayerID, invalidSize, "https://example.com/foo.png")
		require.Equal(t, core.Unknown, a.FileType)
		_, err := a.Path()
		require.Error(t, err, "Path() must error when FileType is Unknown")
	})

	t.Run("NewTeamLogo", func(t *testing.T) {
		t.Parallel()
		a := NewTeamLogo(testTeamID, testURL)
		require.Equal(t, core.TeamLogo, a.FileType)
		require.Equal(t, []int64{testTeamID}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})

	t.Run("NewYahooTeamLogo", func(t *testing.T) {
		t.Parallel()
		a := NewYahooTeamLogo(testLeagueID, testYahooTeam, testURL)
		require.Equal(t, core.YahooTeamLogo, a.FileType)
		require.Equal(t, []int64{int64(testLeagueID), int64(testYahooTeam)}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})

	t.Run("NewYahooLeagueLogo", func(t *testing.T) {
		t.Parallel()
		a := NewYahooLeagueLogo(testLeagueID, testURL)
		require.Equal(t, core.YahooLeagueLogo, a.FileType)
		require.Equal(t, []int64{int64(testLeagueID)}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})

	t.Run("NewYahooManagerImage", func(t *testing.T) {
		t.Parallel()
		a := NewYahooManagerImage(testLeagueID, testYahooTeam, testManagerID, testURL)
		require.Equal(t, core.YahooManagerImage, a.FileType)
		require.Equal(t, []int64{int64(testLeagueID), int64(testYahooTeam), int64(testManagerID)}, a.IDs)
		require.Equal(t, testURL, a.URL)
	})
}
