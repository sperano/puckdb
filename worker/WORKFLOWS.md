# Workflow/Activity Hierarchy

## Workflow → Activity Hierarchy

### 1. InitializeWorkflow

Downloads and upserts reference data (franchises, seasons, league structure).

```
InitializeWorkflow
├── DownloadFranchisesActivity
├── UpsertFranchisesActivity
├── DownloadSeasonsManifestActivity
├── UpsertSeasonsActivity
└── InitializeSeasonTeamsActivity
    └── (internally calls) DownloadSeasonStandingsActivity
```

### 2. FetchSeasonsWorkflow

Parent workflow for fetching all season data (NHL schedules + Yahoo fantasy data).

```
FetchSeasonsWorkflow
├── FetchSeasonsDataActivity
└── [child] FetchSeasonWorkflow (per season)
    ├── FetchLeagueActivity (per league)
    ├── FetchTeamActivity (per team)
    └── FetchDayActivity (per day, concurrent)
        ├── FetchDailyScheduleActivity
        ├── FetchRosterForTeamOnDayActivity (per team)
        └── FetchTeamSummaryForTeamOnDayActivity (per team)
```

### 3. FetchDayWorkflow

Standalone workflow for fetching a single day's data.

```
FetchDayWorkflow
├── FetchDailyScheduleActivity
├── FetchRosterForTeamOnDayActivity (per team)
└── FetchTeamSummaryForTeamOnDayActivity (per team)
```

### 4. FetchYahooPlayersWorkflow

Downloads all Yahoo player pages (uses ContinueAsNew for large ID ranges).

```
FetchYahooPlayersWorkflow
└── FetchYahooPlayerBatchActivity (batched, uses ContinueAsNew)
```

### 5. ImportSeasonsWorkflow

Imports boxscore data from cache into the database.

```
ImportSeasonsWorkflow
├── FetchSeasonsDataActivity
└── ImportBoxscoresForDateActivity (per day, concurrent)
```

### 6. ImportNHLTeamsAndPlayersWorkflow

Extracts teams and player IDs from cached boxscores.

```
ImportNHLTeamsAndPlayersWorkflow
├── FetchSeasonsDataActivity
└── ExtractBoxscoreDataForSeasonActivity (per season, concurrent)
```

### 7. ProcessPlayersWorkflow

Unified workflow that downloads player landing pages, matches with Yahoo data, and imports to database. Uses ContinueAsNew between 4 phases.

```
ProcessPlayersWorkflow
│
├── Phase 1: Extract IDs
│   └── [child] ImportNHLTeamsAndPlayersWorkflow
│
├── Phase 2: Load Yahoo Pool
│   ├── ListYahooPlayerFilesActivity
│   ├── ParseYahooPlayerBatchActivity (batched, concurrent)
│   └── SaveYahooPlayersToRedisActivity
│
├── Phase 3: Process Players (uses ContinueAsNew for large sets)
│   └── ProcessPlayerBatchActivity (batched, concurrent)
│
└── Phase 4: Verify Unmatched
    ├── LoadUnmatchedYahooPlayersActivity
    ├── VerifyUnmatchedBatchActivity (batched)
    └── CleanupYahooIDPoolActivity
```

---

## Activity Reference

| Activity | File | Called By |
|----------|------|-----------|
| `CleanupYahooIDPoolActivity` | `activity_report_unmatched_yahoo.go` | ProcessPlayersWorkflow (Phase 4) |
| `DownloadFranchisesActivity` | `activity_download_franchises.go` | InitializeWorkflow |
| `DownloadSeasonsManifestActivity` | `activity_initialize_seasons.go` | InitializeWorkflow |
| `DownloadSeasonStandingsActivity` | `activity_initialize_seasons.go` | InitializeSeasonTeamsActivity (internal) |
| `ExtractBoxscoreDataForSeasonActivity` | `activity_extract_boxscore_data.go` | ImportNHLTeamsAndPlayersWorkflow |
| `FetchDailyScheduleActivity` | `activity_fetch_daily_schedule.go` | FetchDayActivity, FetchDayWorkflow |
| `FetchDayActivity` | `activity_fetch_day.go` | FetchSeasonWorkflow |
| `FetchLeagueActivity` | `activity_fetch_league.go` | FetchSeasonWorkflow |
| `FetchRosterForTeamOnDayActivity` | `activity_fetch_roster.go` | FetchDayActivity, FetchDayWorkflow |
| `FetchSeasonsDataActivity` | `activity_fetch_seasons.go` | FetchSeasonsWorkflow, ImportSeasonsWorkflow, ImportNHLTeamsAndPlayersWorkflow |
| `FetchTeamActivity` | `activity_fetch_team.go` | FetchSeasonWorkflow |
| `FetchTeamSummaryForTeamOnDayActivity` | `activity_fetch_team_summary.go` | FetchDayActivity, FetchDayWorkflow |
| `FetchYahooPlayerBatchActivity` | `activity_fetch_yahoo_player.go` | FetchYahooPlayersWorkflow |
| `ImportBoxscoresForDateActivity` | `activity_import_boxscores.go` | ImportSeasonsWorkflow |
| `InitializeSeasonTeamsActivity` | `activity_initialize_seasons.go` | InitializeWorkflow |
| `ListYahooPlayerFilesActivity` | `activity_load_yahoo_id_pool.go` | ProcessPlayersWorkflow (Phase 2) |
| `LoadUnmatchedYahooPlayersActivity` | `activity_report_unmatched_yahoo.go` | ProcessPlayersWorkflow (Phase 4) |
| `ParseYahooPlayerBatchActivity` | `activity_load_yahoo_id_pool.go` | ProcessPlayersWorkflow (Phase 2) |
| `ProcessPlayerBatchActivity` | `activity_process_player_batch.go` | ProcessPlayersWorkflow (Phase 3) |
| `SaveYahooPlayersToRedisActivity` | `activity_load_yahoo_id_pool.go` | ProcessPlayersWorkflow (Phase 2) |
| `UpsertFranchisesActivity` | `activity_upsert_franchises.go` | InitializeWorkflow |
| `UpsertSeasonsActivity` | `activity_initialize_seasons.go` | InitializeWorkflow |
| `UpsertSeasonTeamsActivity` | `activity_initialize_seasons.go` | InitializeSeasonTeamsActivity (internal) |
| `VerifyUnmatchedBatchActivity` | `activity_verify_unmatched.go` | ProcessPlayersWorkflow (Phase 4) |
