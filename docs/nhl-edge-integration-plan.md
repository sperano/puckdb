# NHL Edge API Integration Plan

## Summary

Add NHL Edge tracking stats (skating speed, distance, shot speed, shot locations, zone
time) to puckdb. This data covers per-player, per-goalie, and per-team Edge analytics
available since the 2021-2022 season.

## Endpoint Verification (2026-04-17)

### Working Endpoints (200 OK) — 24 total

**Skater detail** — parameterized by `{playerId}/{season}/{gameType}`:
- `v1/edge/skater-detail/{p}/{s}/{gt}` — combined Edge stats (speed, distance, shots, zones)
- `v1/edge/skater-skating-speed-detail/{p}/{s}/{gt}` — top skating speeds per game
- `v1/edge/skater-skating-distance-detail/{p}/{s}/{gt}` — distance skated per game
- `v1/edge/skater-shot-speed-detail/{p}/{s}/{gt}` — hardest shots per game
- `v1/edge/skater-shot-location-detail/{p}/{s}/{gt}` — shots by rink area
- `v1/edge/skater-zone-time/{p}/{s}/{gt}` — OZ/NZ/DZ time percentages
- `v1/edge/skater-comparison/{p}/{s}/{gt}` — rich composite for head-to-head (shotSpeed, skatingSpeed, distance, shotLocation, zoneTime, zoneStarts)

**Goalie detail** — parameterized by `{goalieId}/{season}/{gameType}`:
- `v1/edge/goalie-detail/{g}/{s}/{gt}` — combined Edge goalie stats
- `v1/edge/goalie-5v5-detail/{g}/{s}/{gt}` — 5v5 save % per game
- `v1/edge/goalie-shot-location-detail/{g}/{s}/{gt}` — saves by rink area
- `v1/edge/goalie-save-percentage-detail/{g}/{s}/{gt}` — overall save % per game
- `v1/edge/goalie-comparison/{g}/{s}/{gt}` — rich composite (shotLocation, savePctg5v5, savePctg)

**Team detail** — parameterized by `{teamId}/{season}/{gameType}`:
- `v1/edge/team-detail/{t}/{s}/{gt}` — combined team Edge stats
- `v1/edge/team-skating-speed-detail/{t}/{s}/{gt}` — top skating speeds (per player)
- `v1/edge/team-skating-distance-detail/{t}/{s}/{gt}` — distance per game (team total)
- `v1/edge/team-shot-speed-detail/{t}/{s}/{gt}` — team shot speed
- `v1/edge/team-shot-location-detail/{t}/{s}/{gt}` — team shots by area
- `v1/edge/team-zone-time-details/{t}/{s}/{gt}` — zone time by strength (all/es/pp/pk) + shot differential
- `v1/edge/team-comparison/{t}/{s}/{gt}` — rich composite (speed, distance, shotLocation, zoneTime, shotDifferential)

**Landing pages** — parameterized by `{season}/{gameType}` only (league-wide leaders):
- `v1/edge/skater-landing/{s}/{gt}` — league leaders: hardest shot, fastest skater, etc.
- `v1/edge/goalie-landing/{s}/{gt}` — league leaders: high-danger save%, etc.
- `v1/edge/team-landing/{s}/{gt}` — league leaders: shot attempts over 90, etc.

**CAT (Catch All Tracking)** — parameterized by `{playerId}/{season}/{gameType}`:
- `v1/cat/edge/skater-detail/{p}/{s}/{gt}` — same as skater-detail minus `distanceMaxGame`
- `v1/cat/edge/goalie-detail/{g}/{s}/{gt}` — same as goalie-detail

**`/now` variants** — all return 307 redirect to current season (usable with follow-redirects).
Pattern from nhl-api-py: when `season` is omitted, append `/now` instead of `/{season}/{gameType}`.

### Dead Endpoints (404/500)

All `*-top-10` leaderboard endpoints are dead even with full path parameters
(`/{positions}/{strength}/{sort-by}/{season}/{gameType}`). Some return 500 instead of 404.

- `v1/edge/skater-speed-top-10` — ❌
- `v1/edge/skater-distance-top-10` — ❌
- `v1/edge/skater-shot-speed-top-10` — ❌
- `v1/edge/skater-shot-location-top-10` — ❌
- `v1/edge/skater-zone-time-top-10` — ❌ (500)
- `v1/edge/goalie-edge-save-pctg-top-10` — ❌
- `v1/edge/goalie-5v5-top-10` — ❌
- `v1/edge/goalie-shot-location-top-10` — ❌
- `v1/edge/team-skating-speed-top-10` — ❌
- `v1/edge/team-skating-distance-top-10` — ❌ (500)
- `v1/edge/team-shot-speed-top-10` — ❌
- `v1/edge/team-shot-location-top-10` — ❌
- `v1/edge/team-zone-time-top-10` — ❌ (500)
- `v1/edge/by-the-numbers` — ❌

The landing pages (`skater-landing`, `goalie-landing`, `team-landing`) serve as the
replacement for top-10 endpoints — they return league-wide leaders in each category.

### Data Availability

Edge stats are available from **2021-2022 onward** (confirmed via `seasonsWithEdgeStats`
in responses). Seasons before that will return empty/404.

### External References

| Reference | Status (Apr 2026) | Notes |
|-----------|-------------------|-------|
| [Zmalski/NHL-API-Reference #69](https://github.com/Zmalski/NHL-API-Reference/issues/69) | Borderline (last commit Nov 2025) | Original Edge endpoint discovery thread. PR #71 added Edge docs. |
| [dfleis/nhl-api-docs](https://github.com/dfleis/nhl-api-docs) | Unmaintained (last commit Sep 2025) | Parsed NHL WADL file; found 66 Edge endpoint variants. Useful as completeness reference. |
| [coreyjs/nhl-api-py](https://github.com/coreyjs/nhl-api-py) | **Active** (last commit Mar 2026) | Python client with 28 Edge methods. Best reference implementation. Apache 2.0. |

The NHL API is formally defined via a WADL (Web Application Description Language) file
at `api-web.nhle.com/application.wadl`. The dfleis repo has a parsed copy in
`data/parsed/endpoints_raw.json` which can be used for completeness verification.

---

## Phase 1: nhl-api-go — Edge Types & Client Methods

**Repo:** `~/code/workspaces/puckdb/nhl-api-go/`

### 1.1 Add Edge Types

Create `edge.go` with response structs matching the live API shapes.

All types must follow library conventions:
- Named types only (no anonymous/inline struct definitions)
- Reuse existing types: `LocalizedString`, `PeriodDescriptor`, `GameType`, `Season`, `PlayerID`, `TeamID`
- Pointer types for optional nested objects (`*EdgeOverlay`)
- JSON struct tags on every field
- One file is acceptable for initial implementation; split to `edge_skater.go`, `edge_goalie.go`, `edge_team.go` if >40 types

**Endpoint coverage by phase:**

| Endpoint | nhl-api-go type | FileType | DB table | Fetch | Import |
|----------|----------------|----------|----------|-------|--------|
| skater-detail | EdgeSkaterDetail | EdgeSkaterDetail | edge_skater_stats + sub-tables | ✓ | ✓ |
| skater-speed-detail | EdgeSkaterSpeedDetail | EdgeSkaterSpeedDetail | — (cache only) | ✓ | — |
| skater-distance-detail | EdgeSkaterDistanceDetail | EdgeSkaterDistanceDetail | — (cache only) | ✓ | — |
| skater-shot-speed-detail | EdgeSkaterShotSpeedDetail | EdgeSkaterShotSpeedDetail | — (cache only) | ✓ | — |
| skater-shot-location-detail | EdgeSkaterShotLocationDetail | EdgeSkaterShotLocationDetail | — (cache only) | ✓ | — |
| skater-zone-time | EdgeSkaterZoneTimeDetail | EdgeSkaterZoneTime | — (cache only) | ✓ | — |
| skater-comparison | EdgeSkaterComparison | EdgeSkaterComparison | — (cache only) | ✓ | — |
| goalie-detail | EdgeGoalieDetail | EdgeGoalieDetail | edge_goalie_stats + sub-tables | ✓ | ✓ |
| goalie-5v5-detail | EdgeGoalie5v5Detail | EdgeGoalie5v5Detail | — (cache only) | ✓ | — |
| goalie-shot-location-detail | EdgeGoalieShotLocationDetail | EdgeGoalieShotLocationDetail | — (cache only) | ✓ | — |
| goalie-save-percentage-detail | EdgeGoalieSavePctgDetail | EdgeGoalieSavePctgDetail | — (cache only) | ✓ | — |
| goalie-comparison | EdgeGoalieComparison | EdgeGoalieComparison | — (cache only) | ✓ | — |
| team-detail | EdgeTeamDetail | EdgeTeamDetail | edge_team_stats + sub-tables | ✓ | ✓ |
| team-speed-detail | EdgeTeamSpeedDetail | EdgeTeamSpeedDetail | — (cache only) | ✓ | — |
| team-distance-detail | EdgeTeamDistanceDetail | EdgeTeamDistanceDetail | — (cache only) | ✓ | — |
| team-shot-speed-detail | EdgeTeamShotSpeedDetail | EdgeTeamShotSpeedDetail | — (cache only) | ✓ | — |
| team-shot-location-detail | EdgeTeamShotLocationDetail | EdgeTeamShotLocationDetail | — (cache only) | ✓ | — |
| team-zone-time-details | EdgeTeamZoneTimeDetails | EdgeTeamZoneTimeDetails | edge_team_zone_time_by_strength | ✓ | ✓ |
| team-comparison | EdgeTeamComparison | EdgeTeamComparison | — (cache only) | ✓ | — |
| skater-landing | EdgeSkaterLanding | EdgeSkaterLanding | — (cache only) | ✓ | — |
| goalie-landing | EdgeGoalieLanding | EdgeGoalieLanding | — (cache only) | ✓ | — |
| team-landing | EdgeTeamLanding | EdgeTeamLanding | — (cache only) | ✓ | — |

CAT endpoints (`cat/edge/skater-detail`, `cat/edge/goalie-detail`) are near-duplicates of regular
detail endpoints and are **not fetched or stored**. If a consumer needs CAT-specific data, add later.

**Shared sub-types:**

```go
// EdgeMeasurement represents a value in both imperial and metric units.
type EdgeMeasurement struct {
    Imperial float64 `json:"imperial"`
    Metric   float64 `json:"metric"`
}

// EdgePercentileStat is a measurement with league-relative percentile.
type EdgePercentileStat struct {
    Imperial   float64         `json:"imperial"`
    Metric     float64         `json:"metric"`
    Percentile float64         `json:"percentile"`
    LeagueAvg  EdgeMeasurement `json:"leagueAvg"`
}

// EdgePercentileStatWithOverlay adds game context to a percentile stat.
type EdgePercentileStatWithOverlay struct {
    Imperial   float64         `json:"imperial"`
    Metric     float64         `json:"metric"`
    Percentile float64         `json:"percentile"`
    LeagueAvg  EdgeMeasurement `json:"leagueAvg"`
    Overlay    *EdgeOverlay    `json:"overlay,omitempty"`
}

// EdgeCountPercentileStat is a count-based stat with percentile and league average.
type EdgeCountPercentileStat struct {
    Value      int     `json:"value"`
    Percentile float64 `json:"percentile"`
    LeagueAvg  struct {
        Value float64 `json:"value"`
    } `json:"leagueAvg"`
}

// EdgeRankStat is a count-based stat with league rank (1-32) instead of percentile.
type EdgeRankStat struct {
    Value    int     `json:"value"`
    Rank     int     `json:"rank"`
    LeagueAvg *struct {
        Value float64 `json:"value"`
    } `json:"leagueAvg,omitempty"`
}

// EdgeRankStatWithOverlay adds game context to a rank-based stat.
type EdgeRankStatWithOverlay struct {
    Imperial  float64         `json:"imperial"`
    Metric    float64         `json:"metric"`
    Rank      int             `json:"rank"`
    LeagueAvg EdgeMeasurement `json:"leagueAvg"`
    Overlay   *EdgeOverlay    `json:"overlay,omitempty"`
}

// EdgeOverlay provides game context for a "best-of" stat.
type EdgeOverlay struct {
    Player           EdgeOverlayPlayer `json:"player"`
    GameDate         string            `json:"gameDate"`
    AwayTeam         EdgeOverlayTeam   `json:"awayTeam"`
    HomeTeam         EdgeOverlayTeam   `json:"homeTeam"`
    GameOutcome      *GameOutcome      `json:"gameOutcome,omitempty"`
    PeriodDescriptor PeriodDescriptor  `json:"periodDescriptor"`
    TimeInPeriod     string            `json:"timeInPeriod"`
    GameType         int               `json:"gameType"`
}

type EdgeOverlayPlayer struct {
    FirstName LocalizedString `json:"firstName"`
    LastName  LocalizedString `json:"lastName"`
}

type EdgeOverlayTeam struct {
    Abbrev string `json:"abbrev"`
    Score  int    `json:"score"`
}

// EdgeSeasonAvailability indicates which seasons/game types have Edge data.
type EdgeSeasonAvailability struct {
    ID        int   `json:"id"`
    GameTypes []int `json:"gameTypes"`
}

// EdgeTeamLogo contains light/dark logo URLs.
type EdgeTeamLogo struct {
    Light string `json:"light"`
    Dark  string `json:"dark"`
}

// EdgeTeamInfo is the team metadata embedded in Edge responses.
type EdgeTeamInfo struct {
    ID                         int             `json:"id"`
    CommonName                 LocalizedString `json:"commonName"`
    PlaceNameWithPreposition   LocalizedString `json:"placeNameWithPreposition"`
    Abbrev                     string          `json:"abbrev"`
    TeamLogo                   EdgeTeamLogo    `json:"teamLogo"`
    Slug                       string          `json:"slug"`
    Conference                 string          `json:"conference"`
    Division                   string          `json:"division"`
    Wins                       int             `json:"wins"`
    Losses                     int             `json:"losses"`
    OTLosses                   int             `json:"otLosses"`
    GamesPlayed                int             `json:"gamesPlayed"`
    Points                     int             `json:"points"`
}
```

**Skater types:**

```go
// EdgeSkaterDetail is the response from v1/edge/skater-detail/{p}/{s}/{gt}.
type EdgeSkaterDetail struct {
    Player              EdgeSkaterPlayer              `json:"player"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability      `json:"seasonsWithEdgeStats"`
    TopShotSpeed        EdgePercentileStatWithOverlay  `json:"topShotSpeed"`
    SkatingSpeed        EdgeSkaterSpeed               `json:"skatingSpeed"`
    TotalDistanceSkated EdgePercentileStat             `json:"totalDistanceSkated"`
    DistanceMaxGame     EdgePercentileStatWithOverlay  `json:"distanceMaxGame"`
    SogSummary          []EdgeSkaterSogSummary         `json:"sogSummary"`
    SogDetails          []EdgeSogAreaDetail            `json:"sogDetails"`
    ZoneTimeDetails     EdgeSkaterZoneTimeSummary      `json:"zoneTimeDetails"`
}

type EdgeSkaterPlayer struct {
    ID           int             `json:"id"`
    FirstName    LocalizedString `json:"firstName"`
    LastName     LocalizedString `json:"lastName"`
    BirthDate    string          `json:"birthDate"`
    ShootsCatches string         `json:"shootsCatches"`
    SweaterNumber int            `json:"sweaterNumber"`
    Position     string          `json:"position"`
    Slug         string          `json:"slug"`
    Headshot     string          `json:"headshot"`
    Goals        int             `json:"goals"`
    Assists      int             `json:"assists"`
    Points       int             `json:"points"`
    GamesPlayed  int             `json:"gamesPlayed"`
    Team         EdgeTeamInfo    `json:"team"`
}

type EdgeSkaterSpeed struct {
    SpeedMax    EdgePercentileStatWithOverlay `json:"speedMax"`
    BurstsOver20 EdgeCountPercentileStat      `json:"burstsOver20"`
}

type EdgeSkaterSogSummary struct {
    LocationCode             string  `json:"locationCode"` // "all", "high", "long", "mid"
    Shots                    int     `json:"shots"`
    ShotsPercentile          float64 `json:"shotsPercentile"`
    ShotsLeagueAvg           float64 `json:"shotsLeagueAvg"`
    Goals                    int     `json:"goals"`
    GoalsPercentile          float64 `json:"goalsPercentile"`
    GoalsLeagueAvg           float64 `json:"goalsLeagueAvg"`
    ShootingPctg             float64 `json:"shootingPctg"`
    ShootingPctgPercentile   float64 `json:"shootingPctgPercentile"`
    ShootingPctgLeagueAvg    float64 `json:"shootingPctgLeagueAvg"`
}

type EdgeSogAreaDetail struct {
    Area             string  `json:"area"` // "Crease", "High Slot", "L Circle", etc.
    Shots            int     `json:"shots,omitempty"`      // skater: sog
    ShootingPctg     float64 `json:"shootingPctg,omitempty"`
    ShotsPercentile  float64 `json:"shotsPercentile,omitempty"`
}

type EdgeSkaterZoneTimeSummary struct {
    OffensiveZonePctg        float64 `json:"offensiveZonePctg"`
    OffensiveZonePercentile  float64 `json:"offensiveZonePercentile"`
    OffensiveZoneLeagueAvg   float64 `json:"offensiveZoneLeagueAvg"`
    OffensiveZoneEvPctg      float64 `json:"offensiveZoneEvPctg"`
    OffensiveZoneEvPercentile float64 `json:"offensiveZoneEvPercentile"`
    OffensiveZoneEvLeagueAvg float64 `json:"offensiveZoneEvLeagueAvg"`
    NeutralZonePctg          float64 `json:"neutralZonePctg"`
    NeutralZonePercentile    float64 `json:"neutralZonePercentile"`
    NeutralZoneLeagueAvg     float64 `json:"neutralZoneLeagueAvg"`
    DefensiveZonePctg        float64 `json:"defensiveZonePctg"`
    DefensiveZonePercentile  float64 `json:"defensiveZonePercentile"`
    DefensiveZoneLeagueAvg   float64 `json:"defensiveZoneLeagueAvg"`
}

// Sub-detail response types (cached on filesystem, not imported to DB)
EdgeSkaterSpeedDetail        { TopSkatingSpeeds []EdgeSpeedEntry }
EdgeSkaterDistanceDetail     { SkatingDistanceLast10 []EdgeDistanceEntry }
EdgeSkaterShotSpeedDetail    { HardestShots []EdgeShotSpeedEntry }
EdgeSkaterShotLocationDetail { ShotLocationDetails []EdgeShotLocationEntry }
EdgeSkaterZoneTimeDetail     { ZoneTimeDetails []EdgeZoneTimeEntry }  // by strengthCode

// EdgeSkaterComparison is the response from v1/edge/skater-comparison/{p}/{s}/{gt}.
// Rich composite — data overlaps with skater-detail but adds zoneStarts and
// shotDifferential. Cached on filesystem only, not imported to DB.
type EdgeSkaterComparison struct {
    Player               EdgeSkaterPlayer             `json:"player"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability      `json:"seasonsWithEdgeStats"`
    ShotSpeedDetails     interface{}                   `json:"shotSpeedDetails"`
    SkatingSpeedDetails  interface{}                   `json:"skatingSpeedDetails"`
    SkatingDistanceLast10 interface{}                  `json:"skatingDistanceLast10"`
    SkatingDistanceDetails interface{}                 `json:"skatingDistanceDetails"`
    ShotLocationDetails  interface{}                   `json:"shotLocationDetails"`
    ShotLocationTotals   interface{}                   `json:"shotLocationTotals"`
    ZoneTimeDetails      interface{}                   `json:"zoneTimeDetails"`
    ZoneStarts           interface{}                   `json:"zoneStarts"`
}
// Note: comparison types use interface{} for sub-structures since they are display-only.
// Replace with concrete types if DB import is needed later.

// EdgeSkaterLanding is the response from v1/edge/skater-landing/{s}/{gt}.
// League-wide leaders in each Edge category. Cached on filesystem only.
type EdgeSkaterLanding struct {
    Leaders map[string]interface{} `json:"leaders"`
}
```

**Goalie types:**

```go
// EdgeGoalieDetail is the response from v1/edge/goalie-detail/{g}/{s}/{gt}.
type EdgeGoalieDetail struct {
    Player               EdgeGoaliePlayer                `json:"player"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability         `json:"seasonsWithEdgeStats"`
    Stats                EdgeGoalieStatsSummary           `json:"stats"`
    ShotLocationSummary  []EdgeGoalieShotLocationSummary  `json:"shotLocationSummary"`
    ShotLocationDetails  []EdgeGoalieShotLocationArea     `json:"shotLocationDetails"`
}

type EdgeGoaliePlayer struct {
    ID              int             `json:"id"`
    FirstName       LocalizedString `json:"firstName"`
    LastName        LocalizedString `json:"lastName"`
    BirthDate       string          `json:"birthDate"`
    ShootsCatches   string          `json:"shootsCatches"`
    SweaterNumber   int             `json:"sweaterNumber"`
    Slug            string          `json:"slug"`
    Headshot        string          `json:"headshot"`
    Wins            int             `json:"wins"`
    Losses          int             `json:"losses"`
    OvertimeLosses  int             `json:"overtimeLosses"`
    GoalsAgainstAvg float64         `json:"goalsAgainstAvg"`
    SavePctg        float64         `json:"savePctg"`
    GamesPlayed     int             `json:"gamesPlayed"`
    Team            EdgeTeamInfo    `json:"team"`
}

type EdgeGoalieStatsSummary struct {
    GoalsAgainstAvg      EdgeGoalieStatEntry `json:"goalsAgainstAvg"`
    GamesAbove900        EdgeGoalieStatEntry `json:"gamesAbove900"`
    GoalDifferentialPer60 EdgeGoalieStatEntry `json:"goalDifferentialPer60"`
    GoalSupportAvg       EdgeGoalieStatEntry `json:"goalSupportAvg"`
    PointPctg            EdgeGoalieStatEntry `json:"pointPctg"`
}

type EdgeGoalieStatEntry struct {
    Value      float64 `json:"value"`
    Percentile float64 `json:"percentile"`
    LeagueAvg  float64 `json:"leagueAvg"`
}

type EdgeGoalieShotLocationSummary struct {
    LocationCode             string  `json:"locationCode"`
    GoalsAgainst             int     `json:"goalsAgainst"`
    GoalsAgainstPercentile   float64 `json:"goalsAgainstPercentile"`
    GoalsAgainstLeagueAvg    float64 `json:"goalsAgainstLeagueAvg"`
    Saves                    int     `json:"saves"`
    SavesPercentile          float64 `json:"savesPercentile"`
    SavesLeagueAvg           float64 `json:"savesLeagueAvg"`
    SavePctg                 float64 `json:"savePctg"`
    SavePctgPercentile       float64 `json:"savePctgPercentile"`
    SavePctgLeagueAvg        float64 `json:"savePctgLeagueAvg"`
}

type EdgeGoalieShotLocationArea struct {
    Area             string  `json:"area"`
    Saves            int     `json:"saves"`
    SavesPercentile  float64 `json:"savesPercentile"`
    SavePctg         float64 `json:"savePctg"`
    SavePctgPercentile float64 `json:"savePctgPercentile"`
}

// Sub-detail response types (cached on filesystem, not imported to DB)
EdgeGoalie5v5Detail          { SavePctg5v5Last10 []EdgeGoalie5v5Entry }
EdgeGoalieShotLocationDetail { ShotLocationDetails []EdgeGoalieShotLocationEntry }
EdgeGoalieSavePctgDetail     { SavePctgLast10 []EdgeGoalieSavePctgEntry, SavePctgDetails []EdgeGoalieSavePctgEntry }

// EdgeGoalieComparison is the response from v1/edge/goalie-comparison/{g}/{s}/{gt}.
// Rich composite — overlaps with goalie-detail, adds per-game breakdowns. Filesystem cache only.
type EdgeGoalieComparison struct {
    Player               EdgeGoaliePlayer              `json:"player"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability       `json:"seasonsWithEdgeStats"`
    ShotLocationSummary  interface{}                    `json:"shotLocationSummary"`
    ShotLocationDetails  interface{}                    `json:"shotLocationDetails"`
    SavePctg5v5Last10    interface{}                    `json:"savePctg5v5Last10"`
    SavePctg5v5Details   interface{}                    `json:"savePctg5v5Details"`
    SavePctgLast10       interface{}                    `json:"savePctgLast10"`
    SavePctgDetails      interface{}                    `json:"savePctgDetails"`
}

// EdgeGoalieLanding is the response from v1/edge/goalie-landing/{s}/{gt}.
type EdgeGoalieLanding struct {
    Leaders map[string]interface{} `json:"leaders"`
}
```

**Team types:**

```go
// EdgeTeamDetail is the response from v1/edge/team-detail/{t}/{s}/{gt}.
type EdgeTeamDetail struct {
    Team                 EdgeTeamInfo              `json:"team"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability   `json:"seasonsWithEdgeStats"`
    ShotSpeed            EdgeTeamShotSpeed          `json:"shotSpeed"`
    SkatingSpeed         EdgeTeamSkatingSpeed       `json:"skatingSpeed"`
    DistanceSkated       EdgeTeamDistance           `json:"distanceSkated"`
    SogSummary           []EdgeTeamSogSummary       `json:"sogSummary"`
    SogDetails           []EdgeTeamSogAreaDetail    `json:"sogDetails"`
    ZoneTimeDetails      EdgeTeamZoneTime           `json:"zoneTimeDetails"`
}

type EdgeTeamShotSpeed struct {
    ShotAttemptsOver90 EdgeRankStat            `json:"shotAttemptsOver90"`
    TopShotSpeed       EdgeRankStatWithOverlay `json:"topShotSpeed"`
}

type EdgeTeamSkatingSpeed struct {
    BurstsOver22 EdgeRankStat            `json:"burstsOver22"`
    BurstsOver20 EdgeRankStat            `json:"burstsOver20"`
    SpeedMax     EdgeRankStatWithOverlay `json:"speedMax"`
}

type EdgeTeamDistance struct {
    Total EdgeRankStat `json:"total"`
}

type EdgeTeamSogSummary struct {
    LocationCode         string  `json:"locationCode"`
    Shots                int     `json:"shots"`
    ShotsRank            int     `json:"shotsRank"`
    ShotsLeagueAvg       float64 `json:"shotsLeagueAvg"`
    Goals                int     `json:"goals"`
    GoalsRank            int     `json:"goalsRank"`
    GoalsLeagueAvg       float64 `json:"goalsLeagueAvg"`
    ShootingPctg         float64 `json:"shootingPctg"`
    ShootingPctgRank     int     `json:"shootingPctgRank"`
    ShootingPctgLeagueAvg float64 `json:"shootingPctgLeagueAvg"`
}

type EdgeTeamSogAreaDetail struct {
    Area      string `json:"area"`
    Shots     int    `json:"shots"`
    ShotsRank int    `json:"shotsRank"`
}

type EdgeTeamZoneTime struct {
    OffensiveZonePctg    float64 `json:"offensiveZonePctg"`
    OffensiveZoneRank    int     `json:"offensiveZoneRank"`
    OffensiveZoneLeagueAvg float64 `json:"offensiveZoneLeagueAvg"`
    OffensiveZoneEvPctg  float64 `json:"offensiveZoneEvPctg"`
    OffensiveZoneEvRank  int     `json:"offensiveZoneEvRank"`
    NeutralZonePctg      float64 `json:"neutralZonePctg"`
    NeutralZoneRank      int     `json:"neutralZoneRank"`
    NeutralZoneLeagueAvg float64 `json:"neutralZoneLeagueAvg"`
    DefensiveZonePctg    float64 `json:"defensiveZonePctg"`
    DefensiveZoneRank    int     `json:"defensiveZoneRank"`
    DefensiveZoneLeagueAvg float64 `json:"defensiveZoneLeagueAvg"`
}

// Sub-detail response types (cached on filesystem, not imported to DB)
EdgeTeamSpeedDetail        { TopSkatingSpeeds []EdgeTeamSpeedEntry }
EdgeTeamDistanceDetail     { SkatingDistanceLast10 []EdgeTeamDistanceEntry }
EdgeTeamShotSpeedDetail    { HardestShots []EdgeTeamShotSpeedEntry }
EdgeTeamShotLocationDetail { ShotLocationDetails []EdgeTeamShotLocationEntry }

// EdgeTeamZoneTimeDetails is the response from v1/edge/team-zone-time-details/{t}/{s}/{gt}.
// This is DISTINCT from the zone time data embedded in team-detail — it breaks down
// zone time by strength code (all/es/pp/pk) and includes shot differential.
// Imported to DB in edge_team_zone_time_by_strength table.
type EdgeTeamZoneTimeDetails struct {
    Team                 EdgeTeamInfo                    `json:"team"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability         `json:"seasonsWithEdgeStats"`
    ZoneTimeDetails      []EdgeTeamZoneTimeByStrength     `json:"zoneTimeDetails"`
    ShotDifferential     []EdgeTeamShotDifferentialEntry  `json:"shotDifferential"`
}

type EdgeTeamZoneTimeByStrength struct {
    StrengthCode         string  `json:"strengthCode"` // "all", "es", "pp", "pk"
    OffensiveZonePctg    float64 `json:"offensiveZonePctg"`
    OffensiveZoneRank    int     `json:"offensiveZoneRank"`
    NeutralZonePctg      float64 `json:"neutralZonePctg"`
    NeutralZoneRank      int     `json:"neutralZoneRank"`
    DefensiveZonePctg    float64 `json:"defensiveZonePctg"`
    DefensiveZoneRank    int     `json:"defensiveZoneRank"`
}

type EdgeTeamShotDifferentialEntry struct {
    StrengthCode         string  `json:"strengthCode"` // "all", "es", "pp", "pk"
    ForPerGame           float64 `json:"forPerGame"`
    ForPerGameRank       int     `json:"forPerGameRank"`
    AgainstPerGame       float64 `json:"againstPerGame"`
    AgainstPerGameRank   int     `json:"againstPerGameRank"`
    DifferentialPerGame  float64 `json:"differentialPerGame"`
    DifferentialPerGameRank int  `json:"differentialPerGameRank"`
}

// EdgeTeamComparison is the response from v1/edge/team-comparison/{t}/{s}/{gt}.
// Rich composite — overlaps with team-detail, adds shotDifferential. Filesystem cache only.
type EdgeTeamComparison struct {
    Team                 EdgeTeamInfo                `json:"team"`
    SeasonsWithEdgeStats []EdgeSeasonAvailability    `json:"seasonsWithEdgeStats"`
    ShotSpeedDetails     interface{}                 `json:"shotSpeedDetails"`
    SkatingSpeedDetails  interface{}                 `json:"skatingSpeedDetails"`
    SkatingDistanceLast10 interface{}                `json:"skatingDistanceLast10"`
    SkatingDistanceDetails interface{}               `json:"skatingDistanceDetails"`
    ShotLocationDetails  interface{}                 `json:"shotLocationDetails"`
    ShotLocationTotals   interface{}                 `json:"shotLocationTotals"`
    ZoneTimeDetails      interface{}                 `json:"zoneTimeDetails"`
    ShotDifferential     interface{}                 `json:"shotDifferential"`
}

// EdgeTeamLanding is the response from v1/edge/team-landing/{s}/{gt}.
type EdgeTeamLanding struct {
    Leaders map[string]interface{} `json:"leaders"`
}
```

Note: Team Edge stats use **rank** (1-32) instead of **percentile** (0-1). The types
reflect this with `EdgeRankStat` vs `EdgePercentileStat`.

### 1.2 Add Client Methods

Add to `client.go` under `// ===== Edge Methods =====` section (all use `EndpointAPIWebV1`):

```go
// ===== Edge Skater Methods =====

func (c *Client) EdgeSkaterDetail(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterDetail, error)
func (c *Client) EdgeSkaterSpeedDetail(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterSpeedDetail, error)
func (c *Client) EdgeSkaterDistanceDetail(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterDistanceDetail, error)
func (c *Client) EdgeSkaterShotSpeedDetail(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterShotSpeedDetail, error)
func (c *Client) EdgeSkaterShotLocationDetail(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterShotLocationDetail, error)
func (c *Client) EdgeSkaterZoneTime(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterZoneTimeDetail, error)
func (c *Client) EdgeSkaterComparison(ctx context.Context, playerID PlayerID, season Season, gameType GameType) (*EdgeSkaterComparison, error)

// ===== Edge Goalie Methods =====

func (c *Client) EdgeGoalieDetail(ctx context.Context, goalieID PlayerID, season Season, gameType GameType) (*EdgeGoalieDetail, error)
func (c *Client) EdgeGoalie5v5Detail(ctx context.Context, goalieID PlayerID, season Season, gameType GameType) (*EdgeGoalie5v5Detail, error)
func (c *Client) EdgeGoalieShotLocationDetail(ctx context.Context, goalieID PlayerID, season Season, gameType GameType) (*EdgeGoalieShotLocationDetail, error)
func (c *Client) EdgeGoalieSavePctgDetail(ctx context.Context, goalieID PlayerID, season Season, gameType GameType) (*EdgeGoalieSavePctgDetail, error)
func (c *Client) EdgeGoalieComparison(ctx context.Context, goalieID PlayerID, season Season, gameType GameType) (*EdgeGoalieComparison, error)

// ===== Edge Team Methods =====

func (c *Client) EdgeTeamDetail(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamDetail, error)
func (c *Client) EdgeTeamSpeedDetail(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamSpeedDetail, error)
func (c *Client) EdgeTeamDistanceDetail(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamDistanceDetail, error)
func (c *Client) EdgeTeamShotSpeedDetail(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamShotSpeedDetail, error)
func (c *Client) EdgeTeamShotLocationDetail(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamShotLocationDetail, error)
func (c *Client) EdgeTeamZoneTimeDetails(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamZoneTimeDetails, error)
func (c *Client) EdgeTeamComparison(ctx context.Context, teamID TeamID, season Season, gameType GameType) (*EdgeTeamComparison, error)

// ===== Edge Landing Methods =====

func (c *Client) EdgeSkaterLanding(ctx context.Context, season Season, gameType GameType) (*EdgeSkaterLanding, error)
func (c *Client) EdgeGoalieLanding(ctx context.Context, season Season, gameType GameType) (*EdgeGoalieLanding, error)
func (c *Client) EdgeTeamLanding(ctx context.Context, season Season, gameType GameType) (*EdgeTeamLanding, error)
```

URL construction pattern:
```go
resource := fmt.Sprintf("edge/skater-detail/%s/%s/%d", playerID.String(), season.APIString(), gameType.Int())
```

Error handling: client methods return raw `APIError` including 404 — callers in puckdb
check `errors.Is(err, nhl.ErrNotFound)` to skip players/seasons without Edge data.

### 1.3 Tests

Add `edge_test.go` with **inline JSON string** deserialization tests (matching library
convention — no external fixture files).

```go
func TestEdgeSkaterDetail_Deserialization(t *testing.T) {
    jsonData := `{ "player": { "id": 8478402, ... }, "topShotSpeed": { ... }, ... }`
    var detail EdgeSkaterDetail
    err := json.Unmarshal([]byte(jsonData), &detail)
    require.NoError(t, err)
    // Validate key fields
}
```

One test function per top-level response type. Test both unmarshaling and marshal round-trip
for types with optional fields.

---

## Phase 2: puckdb — Resource & FileType Definitions

**Repo:** `~/code/workspaces/puckdb/puckdb/`

### 2.1 Add FileTypes

Add to `core/filetype.go`:

```go
EdgeSkaterDetail
EdgeSkaterSpeedDetail
EdgeSkaterDistanceDetail
EdgeSkaterShotSpeedDetail
EdgeSkaterShotLocationDetail
EdgeSkaterZoneTime
EdgeSkaterComparison
EdgeGoalieDetail
EdgeGoalie5v5Detail
EdgeGoalieShotLocationDetail
EdgeGoalieSavePctgDetail
EdgeGoalieComparison
EdgeTeamDetail
EdgeTeamSpeedDetail
EdgeTeamDistanceDetail
EdgeTeamShotSpeedDetail
EdgeTeamShotLocationDetail
EdgeTeamZoneTimeDetails
EdgeTeamComparison
EdgeSkaterLanding
EdgeGoalieLanding
EdgeTeamLanding
```

### 2.2 Add Resource Types

Create `resource/edge.go` with resource types implementing `core.URLResource`,
`core.Parseable[T]`, and `core.Formattable[T]`.

**Storage paths** (following existing conventions):

```
edge/skaters/{playerID}/detail-{season}-{gameType}.json
edge/skaters/{playerID}/speed-{season}-{gameType}.json
edge/skaters/{playerID}/distance-{season}-{gameType}.json
edge/skaters/{playerID}/shot-speed-{season}-{gameType}.json
edge/skaters/{playerID}/shot-location-{season}-{gameType}.json
edge/skaters/{playerID}/zone-time-{season}-{gameType}.json
edge/skaters/{playerID}/comparison-{season}-{gameType}.json
edge/goalies/{goalieID}/detail-{season}-{gameType}.json
edge/goalies/{goalieID}/5v5-{season}-{gameType}.json
edge/goalies/{goalieID}/shot-location-{season}-{gameType}.json
edge/goalies/{goalieID}/save-pctg-{season}-{gameType}.json
edge/goalies/{goalieID}/comparison-{season}-{gameType}.json
edge/teams/{teamID}/detail-{season}-{gameType}.json
edge/teams/{teamID}/speed-{season}-{gameType}.json
edge/teams/{teamID}/distance-{season}-{gameType}.json
edge/teams/{teamID}/shot-speed-{season}-{gameType}.json
edge/teams/{teamID}/shot-location-{season}-{gameType}.json
edge/teams/{teamID}/zone-time-details-{season}-{gameType}.json
edge/teams/{teamID}/comparison-{season}-{gameType}.json
edge/landing/skater-{season}-{gameType}.json
edge/landing/goalie-{season}-{gameType}.json
edge/landing/team-{season}-{gameType}.json
```

**URL construction** — base: `https://api-web.nhle.com/`:

```
v1/edge/skater-detail/{playerID}/{season}/{gameType}
v1/edge/skater-skating-speed-detail/{playerID}/{season}/{gameType}
...etc (matching the verified endpoint list above)
```

---

## Phase 3: Database Schema

### 3.1 Migration: Edge Stats Tables

Create migration `000009_edge_stats.up.sql` / `.down.sql`.

**Strategy:** Store the **summary/detail** (composite) data from the main detail
endpoints. The sub-detail endpoints (speed per game, distance per game) contain
per-game breakdowns useful for display but too granular for DB storage — cache those
on the filesystem/Redis only.

**Conventions** (matching existing schema):
- `game_type` uses the `game_type` enum (not SMALLINT) — defined in migration 000001
- `team_id` is `BIGINT` — matches all NHL-sourced tables
- Team tables use composite FK `(season, team_id) REFERENCES season_teams`
- All tables have `created_at` and `updated_at` timestamps
- Store both imperial and metric units for speed/distance measurements (matching API response)
- Secondary indexes on `(season, game_type)` for leaderboard queries

#### Tables to Create

```sql
-- Skater Edge summary stats (from skater-detail endpoint)
CREATE TABLE edge_skater_stats (
    player_id    BIGINT NOT NULL REFERENCES players(id),
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    -- Skating speed
    top_speed_imperial       REAL,
    top_speed_metric         REAL,
    top_speed_percentile     REAL,
    top_speed_league_avg_imperial REAL,
    top_speed_league_avg_metric REAL,
    bursts_over_20           INT,
    bursts_over_20_percentile REAL,
    bursts_over_20_league_avg REAL,

    -- Distance
    total_distance_imperial  REAL,
    total_distance_metric    REAL,
    total_distance_percentile REAL,
    max_game_distance_imperial REAL,
    max_game_distance_metric REAL,
    max_game_distance_percentile REAL,

    -- Shot speed
    top_shot_speed_imperial  REAL,
    top_shot_speed_metric    REAL,
    top_shot_speed_percentile REAL,
    top_shot_speed_league_avg_imperial REAL,
    top_shot_speed_league_avg_metric REAL,

    -- Zone time (all strengths)
    oz_pctg                  REAL,
    oz_percentile            REAL,
    oz_league_avg            REAL,
    nz_pctg                  REAL,
    nz_percentile            REAL,
    nz_league_avg            REAL,
    dz_pctg                  REAL,
    dz_percentile            REAL,
    dz_league_avg            REAL,

    -- Zone time (even strength)
    oz_ev_pctg               REAL,
    oz_ev_percentile         REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type)
);

CREATE INDEX idx_edge_skater_stats_season ON edge_skater_stats(season, game_type);

-- Skater shot location breakdown (17 areas per player/season)
CREATE TABLE edge_skater_shot_locations (
    player_id  BIGINT NOT NULL REFERENCES players(id),
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,               -- "Crease", "High Slot", "L Circle", etc.

    sog              INT,
    goals            INT,
    shooting_pctg    REAL,
    sog_percentile   REAL,
    goals_percentile REAL,
    shooting_pctg_percentile REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, area)
);

-- Skater SOG summary by location code (all/high/long/mid)
CREATE TABLE edge_skater_sog_summary (
    player_id       BIGINT NOT NULL REFERENCES players(id),
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    shots                    INT,
    shots_percentile         REAL,
    shots_league_avg         REAL,
    goals                    INT,
    goals_percentile         REAL,
    goals_league_avg         REAL,
    shooting_pctg            REAL,
    shooting_pctg_percentile REAL,
    shooting_pctg_league_avg REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, location_code)
);

-- Goalie Edge summary stats (from goalie-detail endpoint)
CREATE TABLE edge_goalie_stats (
    player_id    BIGINT NOT NULL REFERENCES players(id),
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    gaa_value                REAL,
    gaa_percentile           REAL,
    gaa_league_avg           REAL,
    games_above_900_value    REAL,
    games_above_900_percentile REAL,
    games_above_900_league_avg REAL,
    goal_diff_per_60_value   REAL,
    goal_diff_per_60_percentile REAL,
    goal_diff_per_60_league_avg REAL,
    goal_support_avg_value   REAL,
    goal_support_avg_percentile REAL,
    goal_support_avg_league_avg REAL,
    point_pctg_value         REAL,
    point_pctg_percentile    REAL,
    point_pctg_league_avg    REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type)
);

CREATE INDEX idx_edge_goalie_stats_season ON edge_goalie_stats(season, game_type);

-- Goalie shot location summary (all/high/long/mid)
CREATE TABLE edge_goalie_shot_location_summary (
    player_id       BIGINT NOT NULL REFERENCES players(id),
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    goals_against            INT,
    goals_against_percentile REAL,
    goals_against_league_avg REAL,
    saves                    INT,
    saves_percentile         REAL,
    saves_league_avg         REAL,
    save_pctg                REAL,
    save_pctg_percentile     REAL,
    save_pctg_league_avg     REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, location_code)
);

-- Goalie shot location detail (17 areas)
CREATE TABLE edge_goalie_shot_locations (
    player_id  BIGINT NOT NULL REFERENCES players(id),
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,

    saves              INT,
    saves_percentile   REAL,
    save_pctg          REAL,
    save_pctg_percentile REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, area)
);

-- Team Edge summary stats (from team-detail endpoint)
CREATE TABLE edge_team_stats (
    team_id      BIGINT NOT NULL,
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    -- Shot speed
    shot_attempts_over_90    INT,
    shot_attempts_over_90_rank INT,
    top_shot_speed_imperial  REAL,
    top_shot_speed_metric    REAL,
    top_shot_speed_rank      INT,

    -- Skating speed
    bursts_over_22           INT,
    bursts_over_22_rank      INT,
    bursts_over_20           INT,
    bursts_over_20_rank      INT,
    speed_max_imperial       REAL,
    speed_max_metric         REAL,
    speed_max_rank           INT,

    -- Distance
    total_distance_imperial  REAL,
    total_distance_metric    REAL,
    total_distance_rank      INT,

    -- Zone time
    oz_pctg                  REAL,
    oz_rank                  INT,
    oz_league_avg            REAL,
    oz_ev_pctg               REAL,
    oz_ev_rank               INT,
    nz_pctg                  REAL,
    nz_rank                  INT,
    nz_league_avg            REAL,
    dz_pctg                  REAL,
    dz_rank                  INT,
    dz_league_avg            REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type)
);

CREATE INDEX idx_edge_team_stats_season ON edge_team_stats(season, game_type);

-- Team SOG summary (all/high/long/mid)
CREATE TABLE edge_team_sog_summary (
    team_id         BIGINT NOT NULL,
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    shots               INT,
    shots_rank          INT,
    shots_league_avg    REAL,
    goals               INT,
    goals_rank          INT,
    goals_league_avg    REAL,
    shooting_pctg       REAL,
    shooting_pctg_rank  INT,
    shooting_pctg_league_avg REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, location_code)
);

-- Team shot location detail (17 areas)
CREATE TABLE edge_team_shot_locations (
    team_id    BIGINT NOT NULL,
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    shots      INT,
    shots_rank INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, area)
);

-- Team zone time by strength code (from team-zone-time-details endpoint)
-- This is DISTINCT from the aggregate zone time in edge_team_stats — it provides
-- zone time broken down by all/es/pp/pk, which is not available in team-detail.
CREATE TABLE edge_team_zone_time_by_strength (
    team_id         BIGINT NOT NULL,
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    strength_code   TEXT NOT NULL CHECK (strength_code IN ('all', 'es', 'pp', 'pk')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    oz_pctg         REAL,
    oz_rank         INT,
    nz_pctg         REAL,
    nz_rank         INT,
    dz_pctg         REAL,
    dz_rank         INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, strength_code)
);

-- Team shot differential by strength code (from team-zone-time-details endpoint)
CREATE TABLE edge_team_shot_differential (
    team_id              BIGINT NOT NULL,
    season               INT NOT NULL REFERENCES seasons(id),
    game_type            game_type NOT NULL,
    strength_code        TEXT NOT NULL CHECK (strength_code IN ('all', 'es', 'pp', 'pk')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    for_per_game              REAL,
    for_per_game_rank         INT,
    against_per_game          REAL,
    against_per_game_rank     INT,
    differential_per_game     REAL,
    differential_per_game_rank INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, strength_code)
);
```

### 3.2 sqlc Queries

Consolidate into domain-specific files (matching `club_stats.sql` pattern):

```
edge_skater.sql   — upserts + reads for edge_skater_stats, _shot_locations, _sog_summary
edge_goalie.sql   — upserts + reads for edge_goalie_stats, _shot_locations, _shot_location_summary
edge_team.sql     — upserts + reads for edge_team_stats, _sog_summary, _shot_locations, _zone_time_by_strength, _shot_differential
```

All upserts must use `ON CONFLICT ... DO UPDATE` with `IS DISTINCT FROM` guard
(matching existing convention in `club_stats.sql`) to avoid unnecessary writes.

For sub-tables (shot_locations, sog_summary), use **delete+insert** instead of upsert:
delete all rows for `(player_id/team_id, season, game_type)` then batch insert. This
protects against stale rows if the NHL changes the set of areas or location codes.

---

## Phase 4: Fetch & Import Activities

### 4.1 Fetch Activities

Create `worker/nhl/fetch_edge.go`:

**EdgeFetchActivities struct** — same pattern as existing `FetchActivities`:
- `Storage`, `GobCache`, `Download` fields
- One activity method per entity type (skater, goalie, team)

**Skater fetch strategy:**
- Input: `season`, `gameType`, list of player IDs (from roster data)
- For each skater: fetch `skater-detail` endpoint (the composite one)
- Also fetch all sub-detail endpoints + `skater-comparison` in the same activity call (matches `FetchClubStats` pattern — single activity does all related work)
- Write to filesystem, cache in GOB

**Goalie fetch strategy:**
- Same pattern, using goalie IDs from roster data
- Fetch `goalie-detail` + sub-detail endpoints (`5v5-detail`, `shot-location-detail`, `save-percentage-detail`) + `goalie-comparison`

**Team fetch strategy:**
- Input: `season`, `gameType`, list of team IDs
- Fetch `team-detail` + sub-detail endpoints + `team-zone-time-details` + `team-comparison` for each team (32 teams max)

**Landing page fetch strategy:**
- Input: `season`, `gameType`
- Fetch all 3 landing pages: `skater-landing`, `goalie-landing`, `team-landing`
- One call per entity type per season — only 3 calls total (not per-player)
- Fetch at the start of the workflow before per-entity fetches

**Error handling:**
- Check `errors.Is(err, nhl.ErrNotFound)` for 404s — log as expected, continue to next player
- Rate-limit accordingly (these are unauthenticated endpoints)

### 4.2 Import Activities

Create `worker/nhl/import_edge.go`:

**EdgeImportActivities struct** — same pattern as existing `ImportActivities`:
- Read cached Edge JSON from filesystem/GOB cache
- Parse into nhl-api-go types
- Convert `gameType` int to `game_type` enum (matching existing converter pattern)
- Map to sqlc upsert/insert params
- Batch upsert main stats tables; delete+insert sub-tables

Import methods:
- `ImportEdgeSkaterStats(ctx, season, gameType, playerIDs)` → upserts to `edge_skater_stats`; delete+insert to `edge_skater_shot_locations`, `edge_skater_sog_summary`
- `ImportEdgeGoalieStats(ctx, season, gameType, goalieIDs)` → upserts to `edge_goalie_stats`; delete+insert to `edge_goalie_shot_locations`, `edge_goalie_shot_location_summary`
- `ImportEdgeTeamStats(ctx, season, gameType, teamIDs)` → upserts to `edge_team_stats`; delete+insert to `edge_team_sog_summary`, `edge_team_shot_locations`
- `ImportEdgeTeamZoneTimeDetails(ctx, season, gameType, teamIDs)` → delete+insert to `edge_team_zone_time_by_strength`, `edge_team_shot_differential`

---

## Phase 5: Temporal Workflows

### 5.1 Fetch Workflow

Create `worker/workflow/fetch_edge.go`:

**FetchEdgeWorkflow** — child workflow, one per season/gameType:
1. Fetch landing pages (3 calls — league-wide, no entity ID needed)
2. Load roster for the season to get player/goalie/team lists (child loads its own roster)
3. Progress group: "Fetching Edge Stats"
4. Fetch team Edge detail + zone-time-details + comparison (32 teams × 3 endpoints)
5. Fetch skater Edge detail + sub-details + comparison (concurrent, ~600-800 players per season)
6. Fetch goalie Edge detail + sub-details + comparison (concurrent, ~80-100 goalies per season)
7. Return `OriginCounts` for progress aggregation

**FetchEdgeSeasonsWorkflow** — parent workflow:
- Filter to seasons >= `MinEdgeStatsSeasonID` (20212022) before spawning children
- Use `iterateSeasons` helper pattern
- Spawns child `FetchEdgeWorkflow` per season (regular season + playoffs)
- Progress tracking via existing `ReportTracker`

```go
const MinEdgeStatsSeasonID = 20212022
```

### 5.2 Import Workflow

Create `worker/workflow/import_edge.go`:

**ImportEdgeWorkflow** — import cached Edge data for a season:
1. Import team Edge stats (from team-detail)
2. Import team zone time + shot differential (from team-zone-time-details)
3. Import skater Edge stats (batched)
4. Import goalie Edge stats (batched)

**ImportEdgeSeasonsWorkflow** — parent over seasons >= `MinEdgeStatsSeasonID`.

### 5.3 Registration

Add to `cmd/worker.go`:
- Register new workflows
- Register new fetch/import activity methods

Add to `cmd/graphql_mutations.go`:
- `fetchEdgeStats` / `importEdgeStats` mutations
- `cancelFetchEdgeStats` / `cancelImportEdgeStats` mutations
- Progress query: `fetchEdgeStatsProgress` / `importEdgeStatsProgress`

---

## Phase 6: GraphQL Schema & Resolvers

### 6.1 Schema Types

Add to `graph/data.graphqls`:

```graphql
type EdgeSkaterStats {
    playerId: Int!
    season: Int!
    gameType: Int!
    topSpeedImperial: Float
    topSpeedMetric: Float
    topSpeedPercentile: Float
    topSpeedLeagueAvgImperial: Float
    topSpeedLeagueAvgMetric: Float
    burstsOver20: Int
    burstsOver20Percentile: Float
    totalDistanceImperial: Float
    totalDistanceMetric: Float
    totalDistancePercentile: Float
    maxGameDistanceImperial: Float
    maxGameDistanceMetric: Float
    topShotSpeedImperial: Float
    topShotSpeedMetric: Float
    topShotSpeedPercentile: Float
    topShotSpeedLeagueAvgImperial: Float
    topShotSpeedLeagueAvgMetric: Float
    ozPctg: Float
    ozPercentile: Float
    ozLeagueAvg: Float
    nzPctg: Float
    nzPercentile: Float
    nzLeagueAvg: Float
    dzPctg: Float
    dzPercentile: Float
    dzLeagueAvg: Float
    ozEvPctg: Float
    ozEvPercentile: Float
    shotLocations: [EdgeShotLocation!]
    sogSummary: [EdgeSogSummary!]
}

type EdgeGoalieStats {
    playerId: Int!
    season: Int!
    gameType: Int!
    gaaValue: Float
    gaaPercentile: Float
    gaaLeagueAvg: Float
    gamesAbove900: Float
    gamesAbove900Percentile: Float
    gamesAbove900LeagueAvg: Float
    goalDiffPer60: Float
    goalDiffPer60Percentile: Float
    goalDiffPer60LeagueAvg: Float
    goalSupportAvg: Float
    goalSupportAvgPercentile: Float
    goalSupportAvgLeagueAvg: Float
    pointPctg: Float
    pointPctgPercentile: Float
    pointPctgLeagueAvg: Float
    shotLocationSummary: [EdgeGoalieShotLocationSummary!]
    shotLocations: [EdgeGoalieShotLocation!]
}

type EdgeTeamStats {
    teamId: Int!
    season: Int!
    gameType: Int!
    topShotSpeedImperial: Float
    topShotSpeedMetric: Float
    topShotSpeedRank: Int
    shotAttemptsOver90: Int
    shotAttemptsOver90Rank: Int
    speedMaxImperial: Float
    speedMaxMetric: Float
    speedMaxRank: Int
    burstsOver22: Int
    burstsOver22Rank: Int
    burstsOver20: Int
    burstsOver20Rank: Int
    totalDistanceImperial: Float
    totalDistanceMetric: Float
    totalDistanceRank: Int
    ozPctg: Float
    ozRank: Int
    ozLeagueAvg: Float
    nzPctg: Float
    nzRank: Int
    nzLeagueAvg: Float
    dzPctg: Float
    dzRank: Int
    dzLeagueAvg: Float
    ozEvPctg: Float
    ozEvRank: Int
    sogSummary: [EdgeTeamSogSummary!]
    shotLocations: [EdgeTeamShotLocation!]
    zoneTimeByStrength: [EdgeTeamZoneTimeByStrength!]
    shotDifferential: [EdgeTeamShotDifferential!]
}

type EdgeShotLocation {
    area: String!
    sog: Int
    goals: Int
    shootingPctg: Float
    sogPercentile: Float
    goalsPercentile: Float
    shootingPctgPercentile: Float
}

type EdgeSogSummary {
    locationCode: String!
    shots: Int
    shotsPercentile: Float
    shotsLeagueAvg: Float
    goals: Int
    goalsPercentile: Float
    goalsLeagueAvg: Float
    shootingPctg: Float
    shootingPctgPercentile: Float
    shootingPctgLeagueAvg: Float
}

type EdgeGoalieShotLocationSummary {
    locationCode: String!
    goalsAgainst: Int
    goalsAgainstPercentile: Float
    goalsAgainstLeagueAvg: Float
    saves: Int
    savesPercentile: Float
    savesLeagueAvg: Float
    savePctg: Float
    savePctgPercentile: Float
    savePctgLeagueAvg: Float
}

type EdgeGoalieShotLocation {
    area: String!
    saves: Int
    savesPercentile: Float
    savePctg: Float
    savePctgPercentile: Float
}

type EdgeTeamSogSummary {
    locationCode: String!
    shots: Int
    shotsRank: Int
    shotsLeagueAvg: Float
    goals: Int
    goalsRank: Int
    goalsLeagueAvg: Float
    shootingPctg: Float
    shootingPctgRank: Int
    shootingPctgLeagueAvg: Float
}

type EdgeTeamShotLocation {
    area: String!
    shots: Int
    shotsRank: Int
}

type EdgeTeamZoneTimeByStrength {
    strengthCode: String!
    ozPctg: Float
    ozRank: Int
    nzPctg: Float
    nzRank: Int
    dzPctg: Float
    dzRank: Int
}

type EdgeTeamShotDifferential {
    strengthCode: String!
    forPerGame: Float
    forPerGameRank: Int
    againstPerGame: Float
    againstPerGameRank: Int
    differentialPerGame: Float
    differentialPerGameRank: Int
}
```

### 6.2 Queries

```graphql
extend type Query {
    edgeSkaterStats(playerId: Int!, season: Int!, gameType: Int): EdgeSkaterStats
    edgeGoalieStats(playerId: Int!, season: Int!, gameType: Int): EdgeGoalieStats
    edgeTeamStats(teamId: Int!, season: Int!, gameType: Int): EdgeTeamStats
}
```

### 6.3 Workflow Mutations

```graphql
extend type Mutation {
    fetchEdgeStats: Boolean!
    importEdgeStats: Boolean!
    cancelFetchEdgeStats: Boolean!
    cancelImportEdgeStats: Boolean!
}

extend type Query {
    fetchEdgeStatsProgress: ProgressReport
    importEdgeStatsProgress: ProgressReport
}
```

---

## Phase 7: MCP Server (puckdb MCP tools)

Add Edge stat tools to the puckdb MCP server so Maurice can query them:

- `get_edge_skater_stats` — by player ID + season
- `get_edge_goalie_stats` — by player ID + season
- `get_edge_team_stats` — by team ID + season

These delegate to the same GraphQL queries. League averages are included in the
response so Maurice can provide context ("McDavid's top speed of 23.97 mph is in
the 98.7th percentile, well above the league average of 22.18 mph").

---

## Phase 8: Update Documentation

### 8.1 Update `nhl-edge-api.md`

- Remove dead endpoints (top-10 leaderboards, by-the-numbers)
- Add verification date and notes
- Document the `/now` redirect behavior (307)
- Add response shape summaries for each working endpoint
- Update references section: mark dfleis and Zmalski repos as unmaintained, note nhl-api-py as active reference

### 8.2 Update `CLAUDE.md`

- Add Edge tables to the schema reference
- Add Edge workflows to the workflow list

---

## Implementation Order

| Step | Description | Effort |
|------|-------------|--------|
| 1 | nhl-api-go: Edge types + client methods + tests | Medium |
| 2 | puckdb: FileTypes + Resource types | Small |
| 3 | puckdb: Database migration + sqlc queries | Medium |
| 4 | puckdb: Fetch activities + import activities | Medium |
| 5 | puckdb: Temporal workflows + registration | Medium |
| 6 | puckdb: GraphQL schema + resolvers | Small |
| 7 | puckdb: MCP tools | Small |
| 8 | Docs update | Small |

Steps 1-3 can be developed and tested independently. Steps 4-5 depend on 1-3.
Step 6 depends on 3. Step 7 depends on 6. Step 8 can happen anytime.

## Design Decisions

**Why store only detail-endpoint data in the DB?**
The sub-detail endpoints (speed per game, distance per game, etc.) contain per-game
breakdowns with 10+ entries each. Storing all of that would add millions of rows for
marginal query value. The detail endpoint already contains the summary/percentile data
that's most useful for comparison and analysis. Sub-detail data stays in the filesystem
cache for on-demand access.

**Why separate tables per entity type (skater/goalie/team)?**
The Edge API returns structurally different data for each. Skaters have percentiles,
goalies have save analytics, teams have ranks (1-32). Forcing them into a single table
would require excessive nullable columns and lose type clarity.

**Why REAL instead of DOUBLE PRECISION?**
Edge stats are percentiles (0-1) and measurements (mph/km). REAL (4 bytes) provides
more than enough precision and halves storage for what will be ~50K+ rows across
all skaters/seasons. Matches existing schema convention (`shooting_pctg`, `save_pctg`
in existing tables are all REAL).

**Why store both imperial and metric?**
The API returns both units and the metric values occasionally have rounding differences
from a naive conversion. Storing both preserves the exact API values and lets GraphQL
consumers pick the unit system without a resolver-side conversion.

**Why delete+insert for sub-tables instead of upsert?**
The shot_locations tables have a variable set of 17 areas and the sog_summary tables
have 4 fixed codes. If the NHL changes the area set (adds or removes one), an upsert
would leave stale rows for removed areas. Delete all rows for the entity's
`(player_id/team_id, season, game_type)` then insert fresh data ensures consistency.

**Why not fetch sub-detail endpoints during import?**
The sub-detail endpoints are useful for UI display ("show me McDavid's top 10 fastest
skates") but don't add analytical value beyond what the summary provides. They are
fetched during the fetch phase (for filesystem caching) but not imported to the database.

**Why fetch but not import comparison endpoints?**
Comparison responses (`skater-comparison`, `goalie-comparison`, `team-comparison`) are rich
composites designed for the NHL website's head-to-head UI. Their data overlaps almost entirely
with the detail endpoints already imported — the only unique fields are `zoneStarts` (skater)
and `shotDifferential` (team). For team shot differential, the `team-zone-time-details` endpoint
provides the same data in a cleaner format that we import directly. The comparison responses
are fetched and cached on the filesystem so Maurice or other consumers can access them for
display-oriented queries, but they don't warrant separate DB tables.

**Why fetch but not import landing pages?**
Landing pages (`skater-landing`, `goalie-landing`, `team-landing`) contain league-wide leaders
in each Edge category. This data is derivable from the per-entity stats already stored in the
DB — a `SELECT ... ORDER BY top_speed_percentile DESC LIMIT 10` achieves the same result.
We fetch and cache them because they're a convenient pre-computed source for Maurice responses,
but there's no need to create separate DB tables for what the detail data already provides.

**Why skip CAT endpoints?**
The CAT (`cat/edge/`) endpoints return nearly identical data to the regular `v1/edge/` detail
endpoints — the only known difference is that `cat/edge/skater-detail` omits `distanceMaxGame`.
Fetching both would double API calls for marginal benefit. If a consumer-specific need arises,
these can be added later with minimal effort.

**Why import team-zone-time-details separately from team-detail?**
The `team-detail` endpoint includes aggregate zone time (all-strengths only). The
`team-zone-time-details` endpoint provides zone time broken down by strength code
(all/es/pp/pk) plus a full shot differential section — data not available anywhere else.
This strength-level breakdown is analytically valuable (e.g., "which teams dominate OZ time
on the power play?") and justifies the two additional tables (`edge_team_zone_time_by_strength`,
`edge_team_shot_differential`).

**Why filter to seasons >= `MinEdgeStatsSeasonID` (20212022) in the workflow?**
The NHL Edge tracking system was introduced in the 2021-2022 season. Earlier seasons
will 404 for every player/team. Filtering upfront avoids ~60,000+ wasted API calls
(~800 players × ~8 pre-Edge seasons × multiple endpoints).
