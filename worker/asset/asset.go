package asset

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
)

// Asset represents an image asset to be fetched from a remote CDN and cached
// locally. It is not a core.Resource; it carries only the information needed
// to compute a cache path and download the file.
type Asset struct {
	FileType core.FileType
	URL      string
	IDs      []int64 // arity depends on FileType; see pathBuilders
}

// Path computes the local cache path for the asset. It derives the file
// extension from the asset URL and delegates path construction to the
// per-FileType builder in pathBuilders.
func (a Asset) Path() (string, error) {
	ext, err := extFromURL(a.URL)
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.FileType.String(), err)
	}
	builder, ok := pathBuilders[a.FileType]
	if !ok {
		return "", fmt.Errorf("no path builder for %s", a.FileType.String())
	}
	return builder(a.IDs, ext)
}

// YahooImageSize selects which Yahoo player image size slot to use.
type YahooImageSize int

const (
	YahooImageSizeSmall YahooImageSize = iota
	YahooImageSizeMedium
	YahooImageSizeLarge
)

// yahooImageSizeFileType maps a YahooImageSize to its corresponding FileType.
// Returns core.Unknown for unrecognised sizes so the downstream pathBuilders
// lookup fails loudly, consistent with extFromURL's strict-allowlist philosophy.
func yahooImageSizeFileType(size YahooImageSize) core.FileType {
	switch size {
	case YahooImageSizeSmall:
		return core.PlayerYahooImageSmall
	case YahooImageSizeMedium:
		return core.PlayerYahooImageMedium
	case YahooImageSizeLarge:
		return core.PlayerYahooImageLarge
	default:
		return core.Unknown
	}
}

// NewPlayerHeadshot constructs an Asset for an NHL player headshot.
func NewPlayerHeadshot(playerID nhl.PlayerID, url string) Asset {
	return Asset{
		FileType: core.PlayerHeadshot,
		URL:      url,
		IDs:      []int64{int64(playerID)},
	}
}

// NewPlayerHeroImage constructs an Asset for an NHL player hero image.
func NewPlayerHeroImage(playerID nhl.PlayerID, url string) Asset {
	return Asset{
		FileType: core.PlayerHeroImage,
		URL:      url,
		IDs:      []int64{int64(playerID)},
	}
}

// NewPlayerYahooImage constructs an Asset for a Yahoo player image. The size
// parameter selects the specific FileType (Small, Medium, or Large).
func NewPlayerYahooImage(playerID nhl.PlayerID, size YahooImageSize, url string) Asset {
	return Asset{
		FileType: yahooImageSizeFileType(size),
		URL:      url,
		IDs:      []int64{int64(playerID)},
	}
}

// NewTeamLogo constructs an Asset for an NHL team logo.
func NewTeamLogo(teamID int64, url string) Asset {
	return Asset{
		FileType: core.TeamLogo,
		URL:      url,
		IDs:      []int64{teamID},
	}
}

// NewYahooTeamLogo constructs an Asset for a Yahoo fantasy team logo.
func NewYahooTeamLogo(leagueID, teamID int32, url string) Asset {
	return Asset{
		FileType: core.YahooTeamLogo,
		URL:      url,
		IDs:      []int64{int64(leagueID), int64(teamID)},
	}
}

// NewYahooLeagueLogo constructs an Asset for a Yahoo fantasy league logo.
func NewYahooLeagueLogo(leagueID int32, url string) Asset {
	return Asset{
		FileType: core.YahooLeagueLogo,
		URL:      url,
		IDs:      []int64{int64(leagueID)},
	}
}

// NewYahooManagerImage constructs an Asset for a Yahoo fantasy team manager image.
func NewYahooManagerImage(leagueID, teamID, managerID int32, url string) Asset {
	return Asset{
		FileType: core.YahooManagerImage,
		URL:      url,
		IDs:      []int64{int64(leagueID), int64(teamID), int64(managerID)},
	}
}
