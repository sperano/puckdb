// Package worker implements Temporal workflows and activities for downloading,
// caching, and importing NHL and Yahoo Fantasy data.
//
// # Workflows
//
// Workflows orchestrate multi-step data pipelines. The main entry points are:
//
//   - [InitializeWorkflow] — seeds reference data (franchises, seasons, standings)
//   - [FetchSeasonsWorkflow] — downloads NHL schedules and Yahoo fantasy data for all seasons
//   - [FetchYahooPlayersWorkflow] — downloads Yahoo player pages (ContinueAsNew for large ranges)
//   - [FetchPlayerLandingsWorkflow] — downloads NHL player landing pages
//   - [ProcessPlayersWorkflow] — matches NHL players with Yahoo IDs, imports to database
//   - [ImportSeasonsWorkflow] — imports cached game data into PostgreSQL
//   - [FetchPlayerLogsWorkflow] / [ImportPlayerLogsWorkflow] — player game log download and import
//   - [ExtractBoxscorePlayersWorkflow] — extracts player IDs from cached boxscores
//
// See docs/workflows.md for the complete workflow-to-activity hierarchy.
//
// # Activity structs
//
// Activities are grouped into receiver structs by domain:
//
//   - [SeasonsActivities] — season manifest, standings, team upsert, day import
//   - [DailyScheduleActivities] — NHL schedule and game data (boxscore, play-by-play, etc.)
//   - [FranchiseActivities] — franchise download and upsert
//   - [YahooActivities] — Yahoo league, team, roster, and player downloads
//   - [BoxscoreActivities] — boxscore player extraction
//   - [PlayerActivities] — player landing pages, game logs, batch processing
//
// Each struct holds its dependencies (Storage, NHLClient, database interfaces)
// as fields, enabling tests to construct them with mocks directly.
//
// # Caching
//
// Activities use a multi-layer cache to avoid redundant API calls:
//
//   - Redis (GobCache) — short-lived, fastest lookup
//   - Filesystem (JuiceFS via store.Storage) — persistent, with optional staleness TTL
//   - Remote API — NHL or Yahoo, fetched only on cache miss
//
// The [resource] package types are used to address cached data. Generic helpers
// like resource.ReadParsed and cache.ReadParsedCached provide type-safe I/O.
//
// # Concurrency
//
// Workflows use Temporal's built-in concurrency primitives:
//
//   - Child workflows for per-season isolation (own history, independent retries)
//   - Worker pools via [ReportTracker].RunWorkerPool for bounded parallelism
//   - ContinueAsNew to avoid hitting Temporal's 50K-event history limit
//
// # Progress tracking
//
// Long-running workflows expose progress via Temporal query handlers and Redis.
// [ProgressReport] and [ReportTracker] manage hierarchical progress bars that
// the GraphQL API polls for UI display.
package worker
