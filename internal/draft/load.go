package draft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ErrNoRules means no rules snapshot was imported for the league and season.
var ErrNoRules = errors.New("no league rules imported")

// Snapshot is one stored version of a league's rules with its provenance.
type Snapshot struct {
	Rules  Rules
	Source Source
	// SourceSeason and SourceLeagueKey name the league the settings were
	// read from: the league itself, or a stand-in's earlier-season source.
	SourceSeason    int
	SourceLeagueKey string
	// FetchedAt is when the settings file was downloaded from Yahoo.
	FetchedAt   time.Time
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	Hash        string
	// Versions counts the distinct rule versions seen for the league.
	Versions int64
}

// OwnedTeam is the logged-in user's team in a league.
type OwnedTeam struct {
	TeamID        int
	Name          string
	DraftPosition int
}

// LeagueReport gathers what the rules comparison shows for one league.
type LeagueReport struct {
	Season    int
	LeagueID  int
	Snapshot  Snapshot
	OwnedTeam *OwnedTeam
	Pool      PoolCoverage
	// Notes records identity or coverage problems found while loading.
	Notes []string
}

// Queries is the database access the loaders need.
type Queries interface {
	GetLatestYahooLeagueRuleSnapshot(ctx context.Context, arg sqlcdb.GetLatestYahooLeagueRuleSnapshotParams) (sqlcdb.YahooLeagueRuleSnapshot, error)
	CountYahooLeagueRuleSnapshots(ctx context.Context, arg sqlcdb.CountYahooLeagueRuleSnapshotsParams) (int64, error)
	GetYahooLeague(ctx context.Context, id int32) (sqlcdb.YahooLeague, error)
	GetYahooOwnedTeam(ctx context.Context, leagueID int32) (sqlcdb.YahooTeam, error)
	ListYahooLeaguePlayersWithNHL(ctx context.Context, leagueKey string) ([]sqlcdb.ListYahooLeaguePlayersWithNHLRow, error)
	// TEMPORARY: back LoadStandInPool (see standin_pool.go,
	// config.LeagueMetadataSource). Remove together with
	// temporary_metadata_from once Yahoo access returns.
	GetSeasonRosterCoverage(ctx context.Context, season int32) (sqlcdb.GetSeasonRosterCoverageRow, error)
	ListSeasonRosterPoolCandidates(ctx context.Context, arg sqlcdb.ListSeasonRosterPoolCandidatesParams) ([]sqlcdb.ListSeasonRosterPoolCandidatesRow, error)
	ListLatestYahooEligiblePositionsByPlayer(ctx context.Context) ([]sqlcdb.ListLatestYahooEligiblePositionsByPlayerRow, error)
}

// LoadSnapshot returns the latest rules version of a league in a season.
func LoadSnapshot(ctx context.Context, q Queries, season, leagueID int) (Snapshot, error) {
	row, err := q.GetLatestYahooLeagueRuleSnapshot(ctx, sqlcdb.GetLatestYahooLeagueRuleSnapshotParams{
		Season: int32(season), LeagueID: int32(leagueID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, fmt.Errorf("season %d league %d: %w; run a Yahoo sync for the season", season, leagueID, ErrNoRules)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load rules for season %d league %d: %w", season, leagueID, err)
	}
	snapshot, err := SnapshotFromRow(row)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Versions, err = q.CountYahooLeagueRuleSnapshots(ctx, sqlcdb.CountYahooLeagueRuleSnapshotsParams{
		Season: int32(season), LeagueID: int32(leagueID),
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("count rule versions for season %d league %d: %w", season, leagueID, err)
	}
	return snapshot, nil
}

// SnapshotFromRow decodes a stored rules snapshot row.
func SnapshotFromRow(row sqlcdb.YahooLeagueRuleSnapshot) (Snapshot, error) {
	var rules Rules
	if err := json.Unmarshal(row.Rules, &rules); err != nil {
		return Snapshot{}, fmt.Errorf("decode rules snapshot %d: %w", row.ID, err)
	}
	return Snapshot{
		Rules: rules, Source: Source(row.Source),
		SourceSeason: int(row.SourceSeason), SourceLeagueKey: row.SourceLeagueKey,
		FetchedAt: row.FetchedAt.Time, FirstSeenAt: row.FirstSeenAt.Time, LastSeenAt: row.LastSeenAt.Time,
		Hash: row.RulesHash,
	}, nil
}

// LoadLeagueReport loads a league's latest rules, the user's team and the
// coverage of its player pool. now and maxAge decide staleness.
func LoadLeagueReport(ctx context.Context, q Queries, season, leagueID int, now time.Time, maxAge time.Duration) (LeagueReport, error) {
	snapshot, err := LoadSnapshot(ctx, q, season, leagueID)
	if err != nil {
		return LeagueReport{}, err
	}
	report := LeagueReport{Season: season, LeagueID: leagueID, Snapshot: snapshot}
	report.OwnedTeam, report.Notes, err = loadOwnedTeam(ctx, q, season, leagueID)
	if err != nil {
		return LeagueReport{}, err
	}
	players, poolNotes, err := LoadLeaguePool(ctx, q, snapshot)
	if err != nil {
		return LeagueReport{}, err
	}
	report.Notes = append(report.Notes, poolNotes...)
	report.Pool = Coverage(players, now, maxAge)
	return report, nil
}

// loadOwnedTeam finds the user's team, refusing yahoo_teams rows that belong
// to another season's league with the same numeric ID.
func loadOwnedTeam(ctx context.Context, q Queries, season, leagueID int) (*OwnedTeam, []string, error) {
	league, err := q.GetYahooLeague(ctx, int32(leagueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, []string{fmt.Sprintf("league %d is not in yahoo_leagues", leagueID)}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load league %d: %w", leagueID, err)
	}
	if int(league.Season) != season {
		return nil, []string{fmt.Sprintf("yahoo_leagues row %d holds season %d (%s), not %d; its teams are not this league's",
			leagueID, league.Season, league.LeagueKey, season)}, nil
	}
	team, err := q.GetYahooOwnedTeam(ctx, int32(leagueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, []string{"your team is unknown: no team owned by the logged-in user was imported"}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load owned team of league %d: %w", leagueID, err)
	}
	return &OwnedTeam{TeamID: int(team.ID), Name: team.Name, DraftPosition: int(team.DraftPosition.Int32)}, nil, nil
}

// LoadPool returns a league's draftable pool with each player's NHL mapping.
func LoadPool(ctx context.Context, q Queries, leagueKey string) ([]PoolPlayer, error) {
	rows, err := q.ListYahooLeaguePlayersWithNHL(ctx, leagueKey)
	if err != nil {
		return nil, fmt.Errorf("load player pool of league %s: %w", leagueKey, err)
	}
	players := make([]PoolPlayer, 0, len(rows))
	for _, row := range rows {
		var nhlPlayerID int64
		if row.NhlPlayerID.Valid {
			nhlPlayerID = row.NhlPlayerID.Int64
		}
		players = append(players, PoolPlayer{
			YahooPlayerID: int(row.PlayerID), PlayerKey: row.PlayerKey, Name: row.FullName,
			Team: row.EditorialTeamAbbr, EligiblePositions: row.EligiblePositions,
			Status: row.Status, StatusFull: row.StatusFull, InjuryNote: row.InjuryNote,
			OnDisabledList: row.OnDisabledList, NHLPlayerID: nhlPlayerID,
			FetchedAt: row.FetchedAt.Time,
		})
	}
	return players, nil
}

// LoadLeaguePool returns a league's draftable pool and any provenance notes,
// choosing the source by the snapshot's rules: LoadStandInPool for a
// TEMPORARY stand-in league (SourceTemporaryStandIn), LoadPool otherwise.
// Every ranking and report caller goes through this instead of LoadPool
// directly, so a stand-in league ranks against NHL rosters instead of
// failing with MISSING_POOL. TEMPORARY: the SourceTemporaryStandIn branch is
// removed together with temporary_metadata_from once Yahoo access returns
// (see standin_pool.go, config.LeagueMetadataSource).
func LoadLeaguePool(ctx context.Context, q Queries, snapshot Snapshot) ([]PoolPlayer, []string, error) {
	if snapshot.Source != SourceTemporaryStandIn {
		players, err := LoadPool(ctx, q, snapshot.Rules.LeagueKey)
		return players, nil, err
	}
	result, err := LoadStandInPool(ctx, q, snapshot.Rules.Season)
	if err != nil {
		return nil, nil, err
	}
	return result.Players, result.Notes(), nil
}
