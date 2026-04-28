package asset

import (
	"context"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
)

// LoadPlayerHeadshotAssets queries all player headshots and converts them to
// Assets. Empty-URL rows are excluded at the SQL layer.
func (a *Activities) LoadPlayerHeadshotAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListPlayerHeadshots(ctx)
	if err != nil {
		return nil, fmt.Errorf("list player headshots: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewPlayerHeadshot(nhl.PlayerID(r.ID), r.HeadshotURL))
	}
	return assets, nil
}

// LoadPlayerHeroImageAssets queries all player hero images and converts them to
// Assets. hero_image_url is nullable; rows without a valid URL are skipped.
func (a *Activities) LoadPlayerHeroImageAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListPlayerHeroImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list player hero images: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		// SQL filters WHERE hero_image_url IS NOT NULL AND hero_image_url <> '',
		// but pgtype.Text.Valid is checked defensively in case the DB returns
		// an unexpected NULL that slips past the filter.
		if !r.HeroImageURL.Valid || r.HeroImageURL.String == "" {
			continue
		}
		assets = append(assets, NewPlayerHeroImage(nhl.PlayerID(r.ID), r.HeroImageURL.String))
	}
	return assets, nil
}

// LoadPlayerYahooImageSmallAssets queries all small Yahoo player images and
// converts them to Assets.
func (a *Activities) LoadPlayerYahooImageSmallAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListPlayerYahooImagesSmall(ctx)
	if err != nil {
		return nil, fmt.Errorf("list player yahoo images small: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewPlayerYahooImage(nhl.PlayerID(r.ID), YahooImageSizeSmall, r.YahooImageSmall))
	}
	return assets, nil
}

// LoadPlayerYahooImageMediumAssets queries all medium Yahoo player images and
// converts them to Assets.
func (a *Activities) LoadPlayerYahooImageMediumAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListPlayerYahooImagesMedium(ctx)
	if err != nil {
		return nil, fmt.Errorf("list player yahoo images medium: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewPlayerYahooImage(nhl.PlayerID(r.ID), YahooImageSizeMedium, r.YahooImageMedium))
	}
	return assets, nil
}

// LoadPlayerYahooImageLargeAssets queries all large Yahoo player images and
// converts them to Assets.
func (a *Activities) LoadPlayerYahooImageLargeAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListPlayerYahooImagesLarge(ctx)
	if err != nil {
		return nil, fmt.Errorf("list player yahoo images large: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewPlayerYahooImage(nhl.PlayerID(r.ID), YahooImageSizeLarge, r.YahooImageLarge))
	}
	return assets, nil
}

// LoadTeamLogoAssets queries one team logo URL per team (latest season wins) and
// converts them to Assets. logo_url is nullable; rows without a valid URL are
// skipped defensively.
func (a *Activities) LoadTeamLogoAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListTeamLogos(ctx)
	if err != nil {
		return nil, fmt.Errorf("list team logos: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		// SQL filters WHERE logo_url IS NOT NULL AND logo_url <> '', but
		// pgtype.Text.Valid is checked defensively.
		if !r.LogoUrl.Valid || r.LogoUrl.String == "" {
			continue
		}
		assets = append(assets, NewTeamLogo(r.TeamID, r.LogoUrl.String))
	}
	return assets, nil
}

// LoadYahooTeamLogoAssets queries all Yahoo fantasy team logos and converts them
// to Assets.
func (a *Activities) LoadYahooTeamLogoAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListYahooTeamLogos(ctx)
	if err != nil {
		return nil, fmt.Errorf("list yahoo team logos: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewYahooTeamLogo(r.LeagueID, r.ID, r.LogoUrl))
	}
	return assets, nil
}

// LoadYahooLeagueLogoAssets queries all Yahoo fantasy league logos and converts
// them to Assets.
func (a *Activities) LoadYahooLeagueLogoAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListYahooLeagueLogos(ctx)
	if err != nil {
		return nil, fmt.Errorf("list yahoo league logos: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewYahooLeagueLogo(r.ID, r.LogoUrl))
	}
	return assets, nil
}

// LoadYahooManagerImageAssets queries all Yahoo fantasy team manager images and
// converts them to Assets.
func (a *Activities) LoadYahooManagerImageAssets(ctx context.Context) ([]Asset, error) {
	rows, err := a.Queries.ListYahooManagerImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list yahoo manager images: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, r := range rows {
		assets = append(assets, NewYahooManagerImage(r.LeagueID, r.TeamID, r.ID, r.ImageUrl))
	}
	return assets, nil
}

// CountPlayerHeadshotAssets returns the number of player headshot rows the
// loader would produce.
func (a *Activities) CountPlayerHeadshotAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountPlayerHeadshots(ctx)
	if err != nil {
		return 0, fmt.Errorf("count player headshots: %w", err)
	}
	return int(n), nil
}

// CountPlayerHeroImageAssets returns the number of player hero-image rows the
// loader would produce.
func (a *Activities) CountPlayerHeroImageAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountPlayerHeroImages(ctx)
	if err != nil {
		return 0, fmt.Errorf("count player hero images: %w", err)
	}
	return int(n), nil
}

// CountPlayerYahooImageSmallAssets returns the number of small Yahoo player
// image rows the loader would produce.
func (a *Activities) CountPlayerYahooImageSmallAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountPlayerYahooImagesSmall(ctx)
	if err != nil {
		return 0, fmt.Errorf("count player yahoo images small: %w", err)
	}
	return int(n), nil
}

// CountPlayerYahooImageMediumAssets returns the number of medium Yahoo player
// image rows the loader would produce.
func (a *Activities) CountPlayerYahooImageMediumAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountPlayerYahooImagesMedium(ctx)
	if err != nil {
		return 0, fmt.Errorf("count player yahoo images medium: %w", err)
	}
	return int(n), nil
}

// CountPlayerYahooImageLargeAssets returns the number of large Yahoo player
// image rows the loader would produce.
func (a *Activities) CountPlayerYahooImageLargeAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountPlayerYahooImagesLarge(ctx)
	if err != nil {
		return 0, fmt.Errorf("count player yahoo images large: %w", err)
	}
	return int(n), nil
}

// CountTeamLogoAssets returns the number of distinct teams with at least one
// non-empty logo URL across any season.
func (a *Activities) CountTeamLogoAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountTeamLogos(ctx)
	if err != nil {
		return 0, fmt.Errorf("count team logos: %w", err)
	}
	return int(n), nil
}

// CountYahooTeamLogoAssets returns the number of Yahoo fantasy team logo rows
// the loader would produce.
func (a *Activities) CountYahooTeamLogoAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountYahooTeamLogos(ctx)
	if err != nil {
		return 0, fmt.Errorf("count yahoo team logos: %w", err)
	}
	return int(n), nil
}

// CountYahooLeagueLogoAssets returns the number of Yahoo fantasy league logo
// rows the loader would produce.
func (a *Activities) CountYahooLeagueLogoAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountYahooLeagueLogos(ctx)
	if err != nil {
		return 0, fmt.Errorf("count yahoo league logos: %w", err)
	}
	return int(n), nil
}

// CountYahooManagerImageAssets returns the number of Yahoo fantasy team manager
// image rows the loader would produce.
func (a *Activities) CountYahooManagerImageAssets(ctx context.Context) (int, error) {
	n, err := a.Queries.CountYahooManagerImages(ctx)
	if err != nil {
		return 0, fmt.Errorf("count yahoo manager images: %w", err)
	}
	return int(n), nil
}
