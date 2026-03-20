# Workflow/Activity Hierarchy

## Workflows

### 1. InitializeWorkflow

Downloads and upserts reference data (franchises, seasons, league structure).

```
InitializeWorkflow                              workflow_initialize.go
├── FranchiseActivities.FetchFranchises         franchise_activities.go
├── FranchiseActivities.UpsertFranchises        franchise_activities.go
├── SeasonsActivities.FetchSeasonsManifest      seasons_activities.go
├── SeasonsActivities.UpsertSeasons             seasons_activities.go
└── SeasonsActivities.InitializeSeasonTeamsActivity  seasons_activities.go
    └── (downloads standings + upserts teams per season)
```

### 2. FetchSeasonsWorkflow

Parent workflow for fetching all season data (NHL schedules + Yahoo fantasy data).

```
FetchSeasonsWorkflow                            workflow_fetch_seasons.go
├── SeasonsActivities.FetchSeasonsManifest      seasons_activities.go
├── DeleteProgressReportBatchActivity (local)   activity_save_progress_report.go
└── [child] FetchSeasonWorkflow (per season)    workflow_fetch_season.go
    ├── YahooActivities.FetchLeague (per league)  yahoo_activities.go
    ├── YahooActivities.FetchTeams (batched)      yahoo_activities.go
    └── DailyScheduleActivities.FetchDay (per day, concurrent)  activity_fetch_day.go
        └── (fetches daily schedule, game data, rosters, team summaries)
```

### 3. FetchYahooPlayersWorkflow

Downloads all Yahoo player pages (uses ContinueAsNew for large ID ranges).

```
FetchYahooPlayersWorkflow                       workflow_fetch_yahoo_players.go
└── YahooActivities.FetchYahooPlayerBatch       yahoo_activities.go
    └── (batched, uses ContinueAsNew)
```

### 4. FetchPlayerLandingsWorkflow

Downloads player landing pages from the NHL API.

```
FetchPlayerLandingsWorkflow                     workflow_fetch_player_landings.go
├── PlayerActivities.LoadAllBoxscorePlayers     player_activities.go
└── PlayerActivities.FetchPlayerLandingsBatch   player_activities.go
    └── (batched, concurrent)
```

### 5. ImportSeasonsWorkflow

Imports cached data into the database, one child workflow per season.

```
ImportSeasonsWorkflow                           workflow_import_seasons.go
├── SeasonsActivities.FetchSeasonsManifest      seasons_activities.go
└── [child] ImportSeasonWorkflow (per season)   workflow_import_season.go
    ├── SeasonsActivities.ImportDay (per day, concurrent)  activity_import_day.go
    ├── SeasonsActivities.ImportYahooLeague     activity_import_yahoo_league.go
    └── SeasonsActivities.ImportYahooTeams      activity_import_yahoo_teams.go
```

### 6. ExtractBoxscorePlayersWorkflow

Extracts teams and player IDs from cached boxscores.

```
ExtractBoxscorePlayersWorkflow                  workflow_extract_boxscore_players.go
├── BoxscoreActivities.ExtractAndSaveBoxscorePlayers  boxscore_activities.go
│   └── (per season, concurrent)
└── ConsolidateBoxscorePlayersActivity          activity_consolidate_players.go
```

### 7. FetchPlayerLogsWorkflow

Downloads player game logs from the NHL API.

```
FetchPlayerLogsWorkflow                         workflow_fetch_player_logs.go
├── PlayerActivities.LoadSeasonBoxscorePlayers  player_activities.go
└── [child] FetchSeasonPlayerLogsWorkflow       workflow_fetch_season_player_logs.go
    ├── PlayerActivities.LoadSeasonBoxscorePlayers  player_activities.go
    └── PlayerActivities.DownloadPlayerGameLogsBatch  player_activities.go
        └── (batched, concurrent)
```

### 8. ImportPlayerLogsWorkflow

Imports player game logs from cache into the database.

```
ImportPlayerLogsWorkflow                        workflow_import_player_logs.go
├── PlayerActivities.CountPlayersForAllSeasons  player_activities.go
└── [child] ImportSeasonPlayerLogsWorkflow      workflow_import_season_player_logs.go
    ├── SeasonsActivities.CollectSeasonPlayerIDs  activity_import_player_game_logs.go
    └── SeasonsActivities.ImportPlayerGameLogsBatch  activity_import_player_game_logs.go
        └── (batched, concurrent)
```

### 9. ProcessPlayersWorkflow

Unified workflow that downloads player landing pages, matches with Yahoo data, and imports to database. Uses ContinueAsNew between 4 phases.

```
ProcessPlayersWorkflow                          workflow_process_players.go
│
├── Phase 1: Load Players
│   └── PlayerActivities.LoadAllBoxscorePlayers  player_activities.go
│
├── Phase 2: Load Yahoo Pool
│   ├── ListYahooPlayerFilesActivity            activity_load_yahoo_id_pool.go
│   ├── ParseYahooPlayerBatchActivity (batched) activity_load_yahoo_id_pool.go
│   └── SaveYahooPlayersToRedisActivity         activity_load_yahoo_id_pool.go
│
├── Phase 3: Process Players (uses ContinueAsNew for large sets)
│   └── ProcessPlayerBatchActivity (batched)    activity_process_player_batch.go
│
└── Phase 4: Verify Unmatched
    ├── LoadUnmatchedYahooPlayersActivity        activity_report_unmatched_yahoo.go
    ├── VerifyUnmatchedBatchActivity (batched)   activity_verify_unmatched.go
    └── CleanupYahooIDPoolActivity               activity_report_unmatched_yahoo.go
```

### 10. Database Admin Workflows

```
DropDatabaseWorkflow     → DropDatabaseActivity      workflow_database_admin.go
MigrateDatabaseWorkflow  → MigrateDatabaseActivity   workflow_database_admin.go
ResetDatabaseWorkflow    → Drop + Migrate             workflow_database_admin.go
FlushRedisWorkflow       → FlushRedisActivity         workflow_database_admin.go
```

---

## Activity Reference

| Activity | Receiver | File |
|----------|----------|------|
| `CleanupYahooIDPoolActivity` | standalone | `activity_report_unmatched_yahoo.go` |
| `CollectSeasonPlayerIDs` | `SeasonsActivities` | `activity_import_player_game_logs.go` |
| `ConsolidateBoxscorePlayersActivity` | standalone | `activity_consolidate_players.go` |
| `CountPlayersForAllSeasons` | `PlayerActivities` | `player_activities.go` |
| `DeleteProgressReportBatchActivity` | standalone (local) | `activity_save_progress_report.go` |
| `DownloadPlayerGameLogsBatch` | `PlayerActivities` | `player_activities.go` |
| `ExtractAndSaveBoxscorePlayers` | `BoxscoreActivities` | `boxscore_activities.go` |
| `FetchDay` | `DailyScheduleActivities` | `activity_fetch_day.go` |
| `FetchFranchises` | `FranchiseActivities` | `franchise_activities.go` |
| `FetchLeague` | `YahooActivities` | `yahoo_activities.go` |
| `FetchPlayerLandingsBatch` | `PlayerActivities` | `player_activities.go` |
| `FetchSeasonsManifest` | `SeasonsActivities` | `seasons_activities.go` |
| `FetchTeams` | `YahooActivities` | `yahoo_activities.go` |
| `FetchYahooPlayerBatch` | `YahooActivities` | `yahoo_activities.go` |
| `ImportDay` | `SeasonsActivities` | `activity_import_day.go` |
| `ImportPlayerGameLogsBatch` | `SeasonsActivities` | `activity_import_player_game_logs.go` |
| `ImportYahooLeague` | `SeasonsActivities` | `activity_import_yahoo_league.go` |
| `ImportYahooTeams` | `SeasonsActivities` | `activity_import_yahoo_teams.go` |
| `InitializeSeasonTeamsActivity` | `SeasonsActivities` | `seasons_activities.go` |
| `ListYahooPlayerFilesActivity` | standalone | `activity_load_yahoo_id_pool.go` |
| `LoadAllBoxscorePlayers` | `PlayerActivities` | `player_activities.go` |
| `LoadSeasonBoxscorePlayers` | `PlayerActivities` | `player_activities.go` |
| `LoadUnmatchedYahooPlayersActivity` | standalone | `activity_report_unmatched_yahoo.go` |
| `ParseYahooPlayerBatchActivity` | standalone | `activity_load_yahoo_id_pool.go` |
| `ProcessPlayerBatchActivity` | standalone | `activity_process_player_batch.go` |
| `SaveYahooPlayersToRedisActivity` | standalone | `activity_load_yahoo_id_pool.go` |
| `UpsertFranchises` | `FranchiseActivities` | `franchise_activities.go` |
| `UpsertSeasons` | `SeasonsActivities` | `seasons_activities.go` |
| `VerifyUnmatchedBatchActivity` | standalone | `activity_verify_unmatched.go` |
