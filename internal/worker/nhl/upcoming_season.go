package nhl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// The upcoming season is the first season of the NHL's standings manifest
// that has not started yet. The season sync otherwise skips it until puck
// drop (see filterSeasons), and season_teams comes from standings the NHL
// publishes only once games are played, so without these activities the
// season has neither clubs nor rosters all preseason — even though the NHL
// already serves its camp rosters. Its clubs are carried forward from the
// prior season and its rosters fetched for each of them.

// UpcomingSeasonQueries is the database access the upcoming-season roster
// import needs beyond SeasonRosterUpserter.
type UpcomingSeasonQueries interface {
	UpsertSeason(ctx context.Context, arg sqlcdb.UpsertSeasonParams) error
	CarryForwardSeasonTeam(ctx context.Context, arg sqlcdb.CarryForwardSeasonTeamParams) (int64, error)
}

// UpcomingSeasonRostersResult reports the upcoming season's roster work.
// Season is the start year, 0 when no upcoming season is in range.
type UpcomingSeasonRostersResult struct {
	Season           int `json:"season"`
	Teams            int `json:"teams"`
	TeamsWithRosters int `json:"teamsWithRosters"`
}

// selectUpcomingSeason returns the earliest manifest season that has not
// started at now and whose start year is within the input range.
func selectUpcomingSeason(seasons []nhlapi.SeasonInfo, input *model.SeasonsInput, now time.Time) (nhlapi.SeasonInfo, bool) {
	var upcoming nhlapi.SeasonInfo
	found := false
	for _, s := range seasons {
		if !s.StandingsStart.Time.After(now) || !seasonStartInRange(s.ID.StartYear(), input) {
			continue
		}
		if !found || s.StandingsStart.Time.Before(upcoming.StandingsStart.Time) {
			upcoming, found = s, true
		}
	}
	return upcoming, found
}

func seasonStartInRange(startYear int, input *model.SeasonsInput) bool {
	if input == nil {
		return true
	}
	if input.StartSeason != nil && startYear < *input.StartSeason {
		return false
	}
	return input.EndSeason == nil || startYear <= *input.EndSeason
}

// upcomingSeason reads the cached manifest (FetchSeasonsManifest ran
// earlier in the same sync) and selects the upcoming season.
func (a *SeasonsActivities) upcomingSeason(ctx context.Context, input *model.SeasonsInput) (nhlapi.SeasonInfo, bool, error) {
	seasons, origin, err := cache.GetSeasons(ctx, a.Storage, a.GobCache)
	if err != nil {
		return nhlapi.SeasonInfo{}, false, fmt.Errorf("read seasons manifest from %s: %w", origin, err)
	}
	upcoming, found := selectUpcomingSeason(seasons, input, time.Now())
	return upcoming, found, nil
}

// FetchUpcomingSeasonRosters caches the upcoming season's roster of every
// NHL club of the prior season. Camp rosters change daily, so each is
// refetched on every run; a club the NHL has no roster for yet is skipped
// and left out of TeamsWithRosters.
func (a *SeasonsActivities) FetchUpcomingSeasonRosters(ctx context.Context, input *model.SeasonsInput) (UpcomingSeasonRostersResult, error) {
	upcoming, found, err := a.upcomingSeason(ctx, input)
	if err != nil || !found {
		return UpcomingSeasonRostersResult{}, err
	}
	startYear := upcoming.ID.StartYear()
	result := UpcomingSeasonRostersResult{Season: startYear}
	teams, err := a.RosterQueries.GetSeasonTeamAbbrevs(ctx, int32(nhlapi.NewSeason(startYear-1).ID()))
	if err != nil {
		return result, fmt.Errorf("get prior season teams: %w", err)
	}
	result.Teams = len(teams)
	season := nhlapi.NewSeason(startYear)
	logger := activity.GetLogger(ctx)
	for _, team := range teams {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("upcoming-roster:%s", team.Abbrev))
		res := resource.SeasonRoster{Season: startYear, TeamAbbrev: team.Abbrev}
		_, _, err := shared.FetchAndCache(ctx, a.Storage, a.GobCache, res, func(ctx context.Context) (*nhlapi.Roster, error) {
			return a.NHLClient.RosterSeason(ctx, team.Abbrev, season)
		})
		if errors.Is(err, nhlapi.ErrNotFound) {
			logger.Warn("No upcoming season roster yet", "team", team.Abbrev, "season", startYear)
			continue
		}
		if err != nil {
			return result, fmt.Errorf("fetch upcoming roster for %s: %w", team.Abbrev, err)
		}
		result.TeamsWithRosters++
	}
	logger.Info("Fetched upcoming season rosters", "season", startYear,
		"teams", result.Teams, "teamsWithRosters", result.TeamsWithRosters)
	return result, nil
}

// ImportUpcomingSeasonRosters stores the upcoming season, carries the prior
// season's NHL clubs forward to it (see carryClubsForward), and imports its
// cached rosters. Every club is carried forward, rostered or not, so a
// partial import reads as incomplete coverage rather than a smaller league.
func (a *SeasonsActivities) ImportUpcomingSeasonRosters(ctx context.Context, input *model.SeasonsInput) (UpcomingSeasonRostersResult, error) {
	upcoming, found, err := a.upcomingSeason(ctx, input)
	if err != nil || !found {
		return UpcomingSeasonRostersResult{}, err
	}
	startYear := upcoming.ID.StartYear()
	result := UpcomingSeasonRostersResult{Season: startYear}
	if err := a.UpcomingQueries.UpsertSeason(ctx, sqlcdb.UpsertSeasonParams{
		ID:             int32(upcoming.ID.ID()),
		StandingsStart: pgtype.Date{Time: upcoming.StandingsStart.Time, Valid: true},
		StandingsEnd:   pgtype.Date{Time: upcoming.StandingsEnd.Time, Valid: true},
	}); err != nil {
		return result, fmt.Errorf("upsert upcoming season %d: %w", upcoming.ID.ID(), err)
	}
	if err := a.carryClubsForward(ctx, upcoming.ID); err != nil {
		return result, err
	}
	counts, err := a.importSeasonRosters(ctx, a.RosterQueries, FetchSeasonRostersInput{Season: startYear})
	result.Teams, result.TeamsWithRosters = counts.teams, counts.teamsWithRosters
	return result, err
}

// carryClubsForward copies every NHL club of the season before upcoming to
// upcoming, under the team ID the club's abbreviation resolves to for
// upcoming (the same lookup the standings import uses).
func (a *SeasonsActivities) carryClubsForward(ctx context.Context, upcoming nhlapi.Season) error {
	prior := nhlapi.NewSeason(upcoming.StartYear() - 1)
	clubs, err := a.RosterQueries.GetSeasonTeamAbbrevs(ctx, int32(prior.ID()))
	if err != nil {
		return fmt.Errorf("get prior season teams: %w", err)
	}
	for _, club := range clubs {
		teamID, err := matching.LookupTeamIDForSeason(club.Abbrev, upcoming.ID())
		if err != nil {
			return fmt.Errorf("season %d team %s: %w", upcoming.ID(), club.Abbrev, err)
		}
		if _, err := a.UpcomingQueries.CarryForwardSeasonTeam(ctx, sqlcdb.CarryForwardSeasonTeamParams{
			ToSeason: int32(upcoming.ID()), ToTeamID: teamID, FromSeason: int32(prior.ID()), FromTeamID: club.TeamID,
		}); err != nil {
			return fmt.Errorf("carry %s forward to season %d: %w", club.Abbrev, upcoming.ID(), err)
		}
	}
	return nil
}
