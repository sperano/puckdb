package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/asset"
	"github.com/spf13/viper"
)

// cacheMetricsSeasonAll is the seasonYear sentinel for season-independent
// metrics (e.g., asset caches). Real NHL season years are >= 1917, so 0 is
// unambiguous. seasonLabel renders this as "all".
const cacheMetricsSeasonAll = 0

// cacheMetrics holds statistics for a single file type within a season
type cacheMetrics struct {
	seasonYear int
	fileType   core.FileType
	expected   int
	found      int
}

// seasonLabel returns the Prometheus season label for this metric. The
// cacheMetricsSeasonAll sentinel renders as cacheSeasonLabelAll so
// season-independent gauges (asset caches) remain distinguishable from real
// season years.
func (s cacheMetrics) seasonLabel() string {
	if s.seasonYear == cacheMetricsSeasonAll {
		return cacheSeasonLabelAll
	}
	return strconv.Itoa(s.seasonYear)
}

type seasonResult struct {
	stats []cacheMetrics
	err   error
	year  int
}

// simpleSeason represents a season with just dates for NHL API-based checks
type simpleSeason struct {
	startYear int
	start     time.Time
	end       time.Time
}

func (s simpleSeason) StartYear() int {
	return s.startYear
}

// fetchSeasonsFromNHL fetches season metadata from the NHL API
func fetchSeasonsFromNHL(ctx context.Context) ([]simpleSeason, error) {
	client := nhl.NewClient()
	seasons, err := client.SeasonStandingManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch seasons from NHL API: %w", err)
	}

	startFilter, endFilter := config.GetSeasonRange()

	var result []simpleSeason
	for _, s := range seasons {
		startYear := s.ID.StartYear()

		if startFilter > 0 && startYear < startFilter {
			continue
		}
		if endFilter > 0 && startYear > endFilter {
			continue
		}

		//start, err := time.Parse(config.DateFormat, s.StandingsStart.Date())
		//if err != nil {
		//	log.Warn().Err(err).Int("season", startYear).Msg("Failed to parse standings start")
		//	continue
		//}
		//end, err := time.Parse(config.DateFormat, s.StandingsEnd)
		//if err != nil {
		//	log.Warn().Err(err).Int("season", startYear).Msg("Failed to parse standings end")
		//	continue
		//}

		result = append(result, simpleSeason{
			startYear: startYear,
			start:     s.StandingsStart.Time,
			end:       s.StandingsEnd.Time,
		})
	}

	return result, nil
}

// cacheCollectorWorkers controls the number of concurrent season-checking
// goroutines. With an indexedStorage in place, per-season Exists checks are
// O(1) memory lookups, so the residual per-season work is daily-schedule
// parsing (Redis-backed, with FS fallback). A small pool still helps because
// it pipelines Redis Get calls and overlaps any remaining FUSE Reads on cold
// gob-cache misses.
const cacheCollectorWorkers = 8

// getAllMetrics gathers cache statistics for all seasons using a bounded
// worker pool, plus the season-independent asset cache aggregates. The
// storage parameter is shared across workers — passing an indexedStorage
// here is what turns this function from O(N_files × FUSE_RTT) into
// O(N_files × map_lookup).
//
// queries may be nil: when nil, asset cache metrics are skipped (the rest of
// the per-season scan still runs). A non-nil queries means a Postgres outage
// during this tick will surface as an error from checkAssetCache and abort
// the whole computation — accepted regression per plan v2 option (a).
func getAllMetrics(
	ctx context.Context,
	redisClient *redis.Client,
	queries *sqlcdb.Queries,
	storage store.Storage,
) ([]cacheMetrics, error) {
	if viper.GetString(config.FlagDataPath) == "" {
		return nil, fmt.Errorf("data-path is required")
	}

	// Fetch seasons from NHL API based on season flags
	seasons, err := fetchSeasonsFromNHL(ctx)
	if err != nil {
		return nil, err
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found matching the specified range")
	}

	// Load Yahoo config (optional - for checking Yahoo files).
	// A missing-or-broken config file is surfaced so the operator knows the
	// cache report will not include Yahoo files; it does not abort the check.
	yahooConfig, err := config.GetYahooSeasonsConfig()
	if err != nil && !errors.Is(err, config.ErrYahooNotConfigured) {
		log.Warn().Err(err).Msg("Yahoo seasons config unreadable; Yahoo files will be skipped")
	}

	work := make(chan simpleSeason, len(seasons))
	results := make(chan seasonResult, len(seasons))
	var wg sync.WaitGroup

	gobCache := cache.NewGobCache(redisClient)
	for range cacheCollectorWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range work {
				cacheData := checkNHLSeasonCache(ctx, storage, gobCache, s)
				if yahooCfg, ok := yahooConfig[s.StartYear()]; ok {
					cacheData = append(cacheData, checkYahooSeasonCache(ctx, storage, s, yahooCfg)...)
				}
				results <- seasonResult{stats: cacheData, err: nil, year: s.StartYear()}
			}
		}()
	}

	for _, season := range seasons {
		work <- season
	}
	close(work)

	go func() {
		wg.Wait()
		close(results)
	}()

	allMetrics := make([]cacheMetrics, 0)
	for result := range results {
		if result.err != nil {
			log.Warn().Err(result.err).Int("season", result.year).Msg("Error checking season")
			continue
		}
		allMetrics = append(allMetrics, result.stats...)
	}

	// Asset caches are season-independent and live outside the per-season worker
	// loop. Skipped entirely when queries is nil so the metrics command can run
	// without a database connection during local development.
	if queries != nil {
		assetMetrics, err := checkAssetCache(ctx, storage, newAssetCacheLoaders(queries))
		if err != nil {
			return nil, err
		}
		allMetrics = append(allMetrics, assetMetrics...)
	}

	return allMetrics, nil
}

// assetLoader produces the row set for a single asset class. The function
// shape lets tests stub loaders without needing a *sqlcdb.Queries mock.
type assetLoader = func(context.Context) ([]asset.Asset, error)

// newAssetCacheLoaders builds the production loader map from a sqlc Queries
// instance. Each entry delegates to the corresponding asset.Activities loader,
// which handles SQL filtering and Asset construction.
func newAssetCacheLoaders(queries *sqlcdb.Queries) map[core.FileType]assetLoader {
	acts := &asset.Activities{Queries: queries}
	return map[core.FileType]assetLoader{
		core.PlayerHeadshot:    acts.LoadPlayerHeadshotAssets,
		core.PlayerHeroImage:   acts.LoadPlayerHeroImageAssets,
		core.PlayerYahooImage:  acts.LoadPlayerYahooImageAssets,
		core.TeamLogo:          acts.LoadTeamLogoAssets,
		core.YahooTeamLogo:     acts.LoadYahooTeamLogoAssets,
		core.YahooLeagueLogo:   acts.LoadYahooLeagueLogoAssets,
		core.YahooManagerImage: acts.LoadYahooManagerImageAssets,
	}
}

// checkAssetCache produces one cacheMetrics entry per loader, in
// core.AllFileTypes declaration order. expected is the number of rows the
// loader returns; found is how many of those local cache files already exist
// on disk. A loader error aborts the whole report rather than leaking partial
// counts that would be indistinguishable from a real cache shortfall.
func checkAssetCache(ctx context.Context, storage store.Storage, loaders map[core.FileType]assetLoader) ([]cacheMetrics, error) {
	out := make([]cacheMetrics, 0, len(loaders))
	for _, ft := range core.AllFileTypes {
		loader, ok := loaders[ft]
		if !ok {
			continue
		}
		assets, err := loader(ctx)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", ft, err)
		}
		labeled := store.WithFileType(storage, ft)
		found := 0
		for _, a := range assets {
			// Honor cancellation between rows. With ~10K rows per class and
			// FUSE-serialized metadata ops, a full scan can take minutes; a
			// SIGTERM during shutdown should not be ignored that long.
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			p, err := a.Path()
			if err != nil {
				// Per-row data issue (bad URL extension, arity mismatch). Log
				// and skip; the row counts as not-found via the unchanged
				// expected total.
				log.Warn().Err(err).Str("file_type", ft.String()).Str("url", a.URL).Msg("Asset path computation failed")
				continue
			}
			if labeled.Exists(ctx, p) {
				found++
			}
		}
		out = append(out, cacheMetrics{
			seasonYear: cacheMetricsSeasonAll,
			fileType:   ft,
			expected:   len(assets),
			found:      found,
		})
	}
	return out, nil
}

// checkNHLSeasonCache checks NHL API files (daily schedule, boxscores,
// play-by-play, shift charts, game stories). Iterates core.AllFileTypes and
// emits one entry per FileType this checker recognizes, in declaration order.
func checkNHLSeasonCache(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season simpleSeason) []cacheMetrics {
	seasonYear := season.StartYear()
	daysInSeason := core.CountDays(season.start, season.end)
	dailyScheduleCount := countDailyScheduleFilesSimple(ctx, storage, season)
	gameFileCounts := countGameFilesSimple(ctx, storage, gobCache, season)

	cacheData := make([]cacheMetrics, 0, 5)
	for _, ft := range core.AllFileTypes {
		var expected, found int
		switch ft {
		case core.DailySchedule:
			expected, found = daysInSeason, dailyScheduleCount
		case core.Boxscore:
			expected, found = gameFileCounts.expectedGames, gameFileCounts.boxscores
		case core.PlayByPlay:
			expected, found = gameFileCounts.expectedGames, gameFileCounts.playByPlay
		case core.ShiftChart:
			expected, found = gameFileCounts.expectedGames, gameFileCounts.shiftCharts
		case core.GameStory:
			expected, found = gameFileCounts.expectedGames, gameFileCounts.gameStories
		default:
			continue
		}
		cacheData = append(cacheData, cacheMetrics{
			seasonYear: seasonYear,
			fileType:   ft,
			expected:   expected,
			found:      found,
		})
	}
	return cacheData
}

// checkYahooSeasonCache checks Yahoo fantasy files (leagues, teams, rosters,
// summaries). Iterates core.AllFileTypes and emits one entry per FileType this
// checker recognizes, in declaration order.
func checkYahooSeasonCache(ctx context.Context, storage store.Storage, nhlSeason simpleSeason, yahooCfg config.Season) []cacheMetrics {
	seasonYear := nhlSeason.startYear
	daysInSeason := core.CountDays(nhlSeason.start, nhlSeason.end)

	leagueCount := countLeagueFiles(ctx, storage, seasonYear, yahooCfg)

	totalTeams := 0
	for _, league := range yahooCfg.Leagues {
		totalTeams += len(league.TeamIDs)
	}
	teamCount := countTeamFiles(ctx, storage, seasonYear, yahooCfg)

	now := time.Now()
	expectedRosters := totalTeams * daysInSeason
	rosterCount := countDailyTeamFiles(ctx, storage, nhlSeason, yahooCfg, now, rosterResource)

	expectedSummaries := totalTeams * daysInSeason
	summaryCount := countDailyTeamFiles(ctx, storage, nhlSeason, yahooCfg, now, teamSummaryResource)

	cacheData := make([]cacheMetrics, 0, 4)
	for _, ft := range core.AllFileTypes {
		var expected, found int
		switch ft {
		case core.League:
			expected, found = len(yahooCfg.Leagues), leagueCount
		case core.Team:
			expected, found = totalTeams, teamCount
		case core.Roster:
			expected, found = expectedRosters, rosterCount
		case core.TeamSummary:
			expected, found = expectedSummaries, summaryCount
		default:
			continue
		}
		cacheData = append(cacheData, cacheMetrics{
			seasonYear: seasonYear,
			fileType:   ft,
			expected:   expected,
			found:      found,
		})
	}
	return cacheData
}

func countLeagueFiles(ctx context.Context, storage store.Storage, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		if resource.Exists(ctx, storage, resource.League{Season: seasonYear, LeagueID: league.LeagueID}) {
			count++
		}
	}
	return count
}

func countTeamFiles(ctx context.Context, storage store.Storage, seasonYear int, cfg config.Season) int {
	count := 0
	for _, league := range cfg.Leagues {
		for _, teamID := range league.TeamIDs {
			if resource.Exists(ctx, storage, resource.Team{Season: seasonYear, LeagueID: league.LeagueID, TeamID: teamID}) {
				count++
			}
		}
	}
	return count
}

// dailyTeamResource builds the cached file of one Yahoo team on one day.
type dailyTeamResource func(leagueID, teamID int, date time.Time) core.Resource

func rosterResource(leagueID, teamID int, date time.Time) core.Resource {
	return resource.Roster{LeagueID: leagueID, TeamID: teamID, Date: date}
}

func teamSummaryResource(leagueID, teamID int, date time.Time) core.Resource {
	return resource.TeamSummary{LeagueID: leagueID, TeamID: teamID, Date: date}
}

// countDailyTeamFiles counts the files build names for every configured team
// on each day of the season, from its start through its end or now, whichever
// comes first (both inclusive).
func countDailyTeamFiles(ctx context.Context, storage store.Storage, nhlSeason simpleSeason, cfg config.Season, now time.Time, build dailyTeamResource) int {
	end := nhlSeason.end
	if end.After(now) {
		end = now
	}

	count := 0
	for current := nhlSeason.start; !current.After(end); current = current.AddDate(0, 0, 1) {
		for _, league := range cfg.Leagues {
			for _, teamID := range league.TeamIDs {
				if resource.Exists(ctx, storage, build(league.LeagueID, teamID, current)) {
					count++
				}
			}
		}
	}
	return count
}

func countDailyScheduleFilesSimple(ctx context.Context, storage store.Storage, season simpleSeason) int {
	count := 0
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		if resource.Exists(ctx, storage, resource.DailySchedule{Date: current}) {
			count++
		}
		current = current.AddDate(0, 0, 1)
	}
	return count
}

// gameFileCounts holds counts for all game-related file types
type gameFileCounts struct {
	expectedGames int
	boxscores     int
	playByPlay    int
	shiftCharts   int
	gameStories   int
}

func countGameFilesSimple(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season simpleSeason) gameFileCounts {
	counts := gameFileCounts{}
	current := season.start
	end := season.end
	if end.After(time.Now()) {
		end = time.Now()
	}

	for !current.After(end) {
		scheduleRes := resource.DailySchedule{Date: current}
		if !resource.Exists(ctx, storage, scheduleRes) {
			current = current.AddDate(0, 0, 1)
			continue
		}

		schedule, _, err := gobCache.ReadParsedCached(ctx, storage, scheduleRes)
		if err != nil {
			log.Warn().Err(err).Time("date", current).Msg("Error parsing daily-schedule file")
			current = current.AddDate(0, 0, 1)
			continue
		}

		gameIDs := make([]nhl.GameID, len(schedule.Games))
		for i, game := range schedule.Games {
			gameIDs[i] = game.ID
		}

		// Filter out preseason games
		gameIDs = filterFinalNonPreseasonGames(gameIDs)

		counts.expectedGames += len(gameIDs)

		for _, gameID := range gameIDs {
			if resource.Exists(ctx, storage, resource.Boxscore{Date: current, GameID: gameID}) {
				counts.boxscores++
			}
			if resource.Exists(ctx, storage, resource.PlayByPlay{Date: current, GameID: gameID}) {
				counts.playByPlay++
			}
			if resource.Exists(ctx, storage, resource.ShiftChart{Date: current, GameID: gameID}) {
				counts.shiftCharts++
			}
			if resource.Exists(ctx, storage, resource.GameStory{Date: current, GameID: gameID}) {
				counts.gameStories++
			}
		}

		current = current.AddDate(0, 0, 1)
	}

	return counts
}

// filterFinalNonPreseasonGames filters out preseason games from a list of game IDs.
func filterFinalNonPreseasonGames(gameIDs []nhl.GameID) []nhl.GameID {
	result := make([]nhl.GameID, 0, len(gameIDs))
	for _, id := range gameIDs {
		gameType, err := id.GameType()
		if err != nil {
			log.Warn().Str("gameid", id.String()).Err(err).Msg("Skipping game with invalid ID")
			continue
		}
		if nhl.GameType(gameType) == nhl.GameTypePreseason {
			continue
		}
		result = append(result, id)
	}
	return result
}

// appendOnDiskOnlyMetrics ensures the rolled-up "Expected"/"Found" totals
// reconcile with the "Total Files" gauge built from the same filesystem walk.
// Without it, totals drift apart in two ways:
//
//  1. FileTypes with no workflow-prescribed expected count (Edge tracking,
//     PlayerLanding, asset caches) contribute to the on-disk total but not to
//     the rolled-up expected/found.
//  2. FileTypes covered by the per-season schedule that have on-disk extras
//     the schedule doesn't enumerate (preseason/exhibition/All-Star games
//     leave surplus Boxscore/PlayByPlay/ShiftChart/GameStory files behind).
//
// For each FileType we emit a single synthetic season=all row with
// expected == found == surplus, where surplus is (idx_count - sum_expected)
// for covered types and idx_count for uncovered types. The per-season
// scheduled rows are left untouched — surplus rows are additive — so genuine
// "missing scheduled file" gaps still surface in the rolled-up found < expected
// delta. Unknown is included so junk files (e.g., .DS_Store, partial
// downloads) reconcile rather than leaving a small permanent gap.
func appendOnDiskOnlyMetrics(cacheData []cacheMetrics, idx *pathIndex) []cacheMetrics {
	coveredExpected := make(map[core.FileType]int, len(cacheData))
	for _, s := range cacheData {
		coveredExpected[s.fileType] += s.expected
	}
	for _, ft := range core.AllFileTypes {
		var count int
		if stats := idx.byType[ft]; stats != nil {
			count = int(stats.count)
		}
		expected, isCovered := coveredExpected[ft]
		surplus := count
		if isCovered {
			surplus = count - expected
			if surplus <= 0 {
				continue
			}
		} else if count == 0 {
			continue
		}
		cacheData = append(cacheData, cacheMetrics{
			seasonYear: cacheMetricsSeasonAll,
			fileType:   ft,
			expected:   surplus,
			found:      surplus,
		})
	}
	return cacheData
}

func countUniqueSeasons(cacheData []cacheMetrics) int {
	seen := make(map[int]bool)
	for _, s := range cacheData {
		seen[s.seasonYear] = true
	}
	return len(seen)
}
