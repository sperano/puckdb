# FetchSeasonsWorkflow Architecture

## Call Tree

```
GraphQL mutation: fetchSeasons(input)  ─or─  CLI: sync command
                        │
                        ▼
 ┌─────────────────────────────────────────────────────────────┐
 │           FetchSeasonsWorkflow  (ID: "fetch-seasons")       │
 │           workflow_fetch_seasons.go                          │
 └─────────────────────┬───────────────────────────────────────┘
                        │
          ┌─────────────┼──────────────────┐
          ▼             ▼                  ▼
   ┌─────────────┐ ┌──────────────┐  ┌──────────────────────────────┐
   │ ACTIVITY:   │ │ ACTIVITY:    │  │ CHILD WORKFLOW (per season): │
   │ FetchSeasons│ │ ClearProgress│  │ FetchSeasonWorkflow          │
   │ Manifest    │ │ Activity     │  │ ID: "fetch-season-{year}"    │
   │             │ │              │  │ workflow_fetch_season.go      │
   │ Returns list│ │ Clears stale │  │                              │
   │ of seasons  │ │ Redis keys   │  │ Up to SeasonConcurrency=2    │
   │ (cache/API) │ │              │  │ running in parallel           │
   └─────────────┘ └──────────────┘  └──────────────┬───────────────┘
                                                     │
                   ┌─────────────────────────────────┼────────────────────┐
                   ▼                                 ▼                    ▼
          ┌─────────────────┐              ┌──────────────────┐  ┌───────────────────┐
          │ ACTIVITY:       │              │ ACTIVITY:        │  │ WORKER POOL:      │
          │ FetchLeague     │              │ FetchTeams       │  │ FetchDayActivity  │
          │ (per league)    │              │ Activity         │  │ (per day in       │
          │                 │              │ (batched, once   │  │  season, ~270)    │
          │ Yahoo fantasy   │              │  per season)     │  │                   │
          │ league HTML     │              │                  │  │ DayConcurrency=3  │
          │                 │              │ Yahoo team pages │  │                   │
          │ yahoo_          │              │                  │  │ activity_fetch_   │
          │ activities.go   │              │ activity_fetch_  │  │ day.go            │
          └─────────────────┘              │ teams.go         │  └────────┬──────────┘
                                           └──────────────────┘           │
                                                              ┌───────────┴───────────┐
                                                              ▼                       ▼
                                                   ┌───────────────────┐  ┌───────────────────┐
                                                   │ FetchDailySchedule│  │ Yahoo per-team    │
                                                   │ (NHL game data)   │  │ per-day downloads │
                                                   │                   │  │                   │
                                                   │ daily_schedule_   │  │ For each team:    │
                                                   │ activities.go     │  │  • FetchRoster    │
                                                   │                   │  │  • FetchTeamSumm. │
                                                   │ For each game:    │  │                   │
                                                   │  • Boxscore       │  └───────────────────┘
                                                   │  • PlayByPlay     │
                                                   │  • ShiftChart     │
                                                   │  • GameStory      │
                                                   └───────────────────┘
```

## Execution Flow

### 1. FetchSeasonsWorkflow (the parent)

**File:** `internal/worker/workflow_fetch_seasons.go`

1. **Calls activity `FetchSeasonsManifest`** — resolves which seasons to fetch using a 3-layer cache with staleness: Redis (1h TTL) → Filesystem (24h staleness TTL, with stale fallback on API failure) → NHL API. Returns `[]nhl.SeasonInfo`.
2. **Calls activity `ClearProgressActivity`** — clears stale Redis progress keys for all child workflow IDs (e.g., `"fetch-season-2023"`, `"fetch-season-2024"`).
3. **Spawns child workflows** — one `FetchSeasonWorkflow` per season, throttled to `SeasonConcurrency` (default **2**) running concurrently.

### 2. FetchSeasonWorkflow (one per season)

**File:** `internal/worker/workflow_fetch_season.go`

For a single season (e.g., 2023-2024):

1. **Calls `FetchLeague`** activity for each configured Yahoo fantasy league (conditional — only if Yahoo config exists for this season).
2. **Calls `FetchTeamsActivity`** once, batched — downloads all Yahoo team pages for the season.
3. **Runs a worker pool** of `FetchDayActivity` calls — one per calendar day in the season (~270 days), with `DayConcurrency` (default **3**) running concurrently.

### 3. FetchDayActivity (one per day — activity, not a workflow)

**File:** `internal/worker/activity_fetch_day.go`

This is a single Temporal activity that internally does several things:

1. **Calls `FetchDailySchedule`** — gets the NHL schedule for that day, then downloads 4 files per game: Boxscore, PlayByPlay, ShiftChart, GameStory.
2. **Fetches Yahoo data** per team (if teams exist): `FetchRoster` + `FetchTeamSummary`.
3. **Updates progress** in Redis via `IncrProgress` (fire-and-forget).

## Configuration

| Aspect | Value |
|--------|-------|
| Season concurrency | 2 (configurable via input) |
| Day concurrency | 3 (configurable via flag) |
| Workflow execution timeout | 24 hours |
| Activity timeout | 10 min (FetchDay gets a longer timeout for Yahoo rate limits) |
| Progress tracking | Two-level: parent tracks seasons, child tracks days, both via Redis + Temporal query handlers |
| Caching | 3-layer: Redis → Filesystem (JuiceFS) → NHL API |

## Design Notes

- **FetchDayActivity is an activity, not a child workflow** — each day's work is small and self-contained, so it doesn't need its own event history or independent retry scope. Keeping it as an activity keeps the history compact.
- **The worker pool pattern** (`tracker.RunWorkerPool`) is a Go-side concurrency limiter within the workflow — it uses a Temporal semaphore to cap how many `FetchDayActivity` calls are in-flight simultaneously, preventing API rate-limit issues.
- **Fan-out/fan-in** — the parent workflow fans out into child workflows (one per season), each of which fans out again into a pool of day-level activities. Child workflows give each season its own execution history and retry scope, avoiding Temporal's 50K-event history limit.

## Fetcher Interfaces and Dependency Injection

The activity layer uses **small, consumer-defined interfaces** for testability. There are two distinct patterns, applied at different architectural layers depending on whether the activity is a standalone function or a struct method.

### Two Testability Patterns

#### Pattern 1: Interface + real/mock struct

Used by **standalone activity functions** that construct their own dependencies internally. A wrapper interface is extracted so the core logic can be tested with a mock.

```
Activity function (standalone func)
  └─ constructs realDayFetcher
  └─ calls fetchDayImpl(fetcher, input)   ← testable seam

Tests:
  └─ constructs MockDayFetcher
  └─ calls fetchDayImpl(fetcher, input)   ← same function, mocked deps
```

**Examples:** `dayFetcher` / `realDayFetcher`, `teamFetcher` / `realTeamFetcher`

#### Pattern 2: Struct with injected interface fields

Used by **struct-based activities** where dependencies are already fields on the struct. Tests construct the struct directly with mocks plugged into the fields — no wrapper interface needed.

```
Production:
  &FranchiseActivities{Storage: realStorage, NHLClient: realClient, Upserter: realDB}

Tests:
  &FranchiseActivities{Storage: store.NewMemStorage(), NHLClient: &MockNHLClient{}, Upserter: &mockUpserter{}}
```

**Examples:** `FranchiseActivities`, `DailyScheduleActivities`, `SeasonsActivities` (which includes `Storage`, `GobCache`, `NHLClient`, `SeasonsUpserter`, `SeasonTeamsUpserter`, `ImportQueries`, and `RedisClient` for the 3-layer cache)

#### When each pattern applies

| Pattern | When | Examples |
|---------|------|----------|
| Interface + real/mock struct | Standalone activity functions that build their own deps | `dayFetcher`, `teamFetcher` |
| Struct with injected interfaces | Struct-based activities where deps are fields | `FranchiseActivities`, `DailyScheduleActivities`, `SeasonsActivities` |

If a standalone activity (Pattern 1) were refactored to be a method on a struct (Pattern 2), the wrapper interface would go away — the struct fields would serve the same testability purpose.

### Interfaces

#### `dayFetcher` — `internal/worker/activity_fetch_day.go`

```go
type dayFetcher interface {
    FetchDailySchedule(ctx context.Context, day time.Time) (FetchDailyScheduleResult, error)
    FetchRoster(ctx context.Context, leagueID, teamID int, day time.Time) error
    FetchTeamSummary(ctx context.Context, leagueID, teamID int, day time.Time) error
}
```

Used by `FetchDayActivity` — bundles everything needed to fetch one day's worth of data.

#### `teamFetcher` — `internal/worker/activity_fetch_teams.go`

```go
type teamFetcher interface {
    FetchTeam(ctx context.Context, season, gameKey, leagueID, teamID int) error
}
```

Used by `FetchTeamsActivity` — abstracts downloading a single Yahoo team page.

#### `NHLClient` — `internal/worker/download.go`

```go
type NHLClient interface {
    PlayerLanding(ctx context.Context, playerID nhl.PlayerID) (*nhl.PlayerLanding, error)
    Boxscore(ctx context.Context, gameID nhl.GameID) (*nhl.Boxscore, error)
    PlayByPlay(ctx context.Context, gameID nhl.GameID) (*nhl.PlayByPlay, error)
    ShiftChart(ctx context.Context, gameID nhl.GameID) (*nhl.ShiftChart, error)
    GameStory(ctx context.Context, gameID nhl.GameID) (*nhl.GameStory, error)
    SeasonSeries(ctx context.Context, gameID nhl.GameID) (*nhl.SeasonSeriesMatchup, error)
    PlayerGameLog(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.PlayerGameLog, error)
    DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error)
    SeasonStandingManifest(ctx context.Context) ([]nhl.SeasonInfo, error)
    LeagueStandingsForSeason(ctx context.Context, season nhl.Season) ([]nhl.Standing, error)
    Franchises(ctx context.Context) ([]nhl.Franchise, error)
    SearchPlayer(ctx context.Context, query string, limit *int) ([]nhl.PlayerSearchResult, error)
}
```

Abstracts the NHL API. Has a compile-time check: `var _ NHLClient = (*nhl.Client)(nil)`.

#### `Downloader` function type — `internal/worker/download.go`

```go
type Downloader func(url string) ([]byte, error)
```

Lowest-level abstraction — just "get bytes from a URL." Real implementation is `DownloadFromYahoo`.

### Real Implementations

#### `realDayFetcher`

```go
type realDayFetcher struct {
    schedule *DailyScheduleActivities   // NHL schedule + game data fetching
    gameKey  int                         // Yahoo game key for the season
    download Downloader                  // func(url) ([]byte, error)
    storage  store.Storage               // filesystem abstraction
}
```

- `FetchDailySchedule()` → delegates to `DailyScheduleActivities`
- `FetchRoster()` / `FetchTeamSummary()` → calls `doDownloadImpl()` with Yahoo URLs

#### `realTeamFetcher`

```go
type realTeamFetcher struct {
    storage  store.Storage
    download Downloader
}
```

- `FetchTeam()` → calls `doDownloadImpl()` with Yahoo team URL

#### `DailyScheduleActivities`

```go
type DailyScheduleActivities struct {
    Storage       store.Storage
    NHLClient     NHLClient
    GobCache      *cache.GobCache
    GameDownloads GameDataDownloaders
}
```

Orchestrates the 3-layer cache hierarchy for NHL game data. `FetchDailySchedule()` checks Redis, then filesystem, then NHL API.

### Composition

```
FetchDayActivity (the Temporal activity function)
  │
  ├─ constructs realDayFetcher with real dependencies
  │    │
  │    ├─ .schedule = DailyScheduleActivities
  │    │      ├─ .NHLClient     → nhl.Client (real NHL API)
  │    │      ├─ .Storage       → store.FSStorage (filesystem)
  │    │      ├─ .GobCache      → cache.GobCache (Redis)
  │    │      └─ .GameDownloads → {Boxscore, PlayByPlay, ShiftChart, GameStory}
  │    │
  │    ├─ .download = DownloadFromYahoo (Downloader func)
  │    └─ .storage  = store.FSStorage
  │
  └─ calls fetcher.FetchDailySchedule(), fetcher.FetchRoster(), etc.
```

### Testing Pattern

The activity function constructs the real fetcher, but the core logic is in a helper that accepts the interface:

```
Production:  FetchDayActivity → builds realDayFetcher → calls fetchDay(fetcher, ...)
Testing:     TestFetchDay     → builds MockDayFetcher → calls fetchDay(fetcher, ...)
```

Mock implementations in `internal/worker/mocks_test.go` use `testify/mock`. A simpler manual mock for `teamFetcher` in `internal/worker/activity_fetch_teams_test.go` records calls and can inject errors at specific indices.

### 3-Layer Cache (inside `DailyScheduleActivities`)

```
Request for a day's schedule
        │
        ▼
  ┌─ Redis (GobCache) ──── HIT ──→ return cached
  │     │ MISS
  │     ▼
  ├─ Filesystem (FSStorage) ── HIT ──→ return cached, populate Redis
  │     │ MISS
  │     ▼
  └─ NHL API ──→ save to Filesystem, populate Redis, return
```
