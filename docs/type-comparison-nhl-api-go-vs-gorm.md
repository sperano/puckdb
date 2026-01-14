# Type Comparison Report: nhl-api-go vs GORM Types

## Summary

The **nhl-api-go** library is significantly more comprehensive - it's designed to capture full NHL API responses, while the GORM types are focused on a subset needed for Yahoo Fantasy Hockey.

---

## Conference Comparison

| Field | nhl-api-go | GORM |
|-------|-----------|------|
| ID | - | ✓ (uint) |
| Abbreviation | ✓ (string) | - |
| Name | ✓ (string) | ✓ (string) |

**Gap:** GORM is missing `Abbreviation`

---

## Division Comparison

| Field | nhl-api-go | GORM |
|-------|-----------|------|
| ID | - | ✓ (uint) |
| Abbreviation | ✓ (string) | - |
| Name | ✓ (string) | ✓ (string) |
| Conference Reference | ✓ (nested) | ✓ (FK) |

**Gap:** GORM is missing `Abbreviation`

---

## Team Comparison

| Field | nhl-api-go | GORM |
|-------|-----------|------|
| ID | ✓ (TeamID) | ✓ (uint) |
| FranchiseID | ✓ (int64) | - |
| FullName | ✓ (string) | - |
| LeagueAbbrev | ✓ (string) | - |
| RawTricode | ✓ (string) | - |
| Tricode/Abbreviation | ✓ (string) | ✓ (string) |
| TeamPlaceName | ✓ (LocalizedString) | - |
| TeamCommonName | ✓ (LocalizedString) | - |
| City | - | ✓ (string) |
| Name | - | ✓ (string) |
| TeamLogo | ✓ (string) | - |
| Conference | ✓ (nested) | ✓ (via division FK) |
| Division | ✓ (nested) | ✓ (FK) |
| NHLHomeLink | - | ✓ (string) |
| YahooHomeLink | - | ✓ (string) |
| SmallLogoURL | - | ✓ (string) |
| LargeLogoURL | - | ✓ (string) |
| AllStars | - | ✓ (bool) |

**Gaps in GORM:** FranchiseID, FullName, LeagueAbbrev, RawTricode, TeamPlaceName, TeamCommonName, TeamLogo

**GORM-only (Yahoo-specific):** NHLHomeLink, YahooHomeLink, SmallLogoURL, LargeLogoURL, AllStars, City/Name split

---

## Game Comparison

| Field | nhl-api-go | GORM |
|-------|-----------|------|
| ID | ✓ (GameID) | ✓ (uint) |
| GameType | ✓ (enum) | - |
| GameDate | ✓ (string) | ✓ (time.Time) |
| Venue | ✓ (LocalizedString) | - |
| VenueLocation | ✓ (LocalizedString) | - |
| StartTimeUTC | ✓ (string) | - |
| Period/Clock Info | ✓ (multiple fields) | - |
| TVBroadcasts | ✓ ([]TVBroadcast) | - |
| GameState | ✓ (GameState enum) | ✓ (string) |
| GameScheduleState | ✓ (enum) | - |
| SpecialEvent | ✓ (*SpecialEvent) | - |
| Team References | ✓ (BoxscoreTeam) | ✓ (FK) |
| Period Scores | ✓ (in team objects) | ✓ (6 fields per team) |
| Summary/Details | ✓ (GameSummary) | - |
| SourceVersion | - | ✓ (time.Time) |

**Major gaps in GORM:** GameType, Venue, VenueLocation, StartTimeUTC, TVBroadcasts, GameScheduleState, SpecialEvent, GameSummary

---

## Types in nhl-api-go with NO GORM equivalent

| Type | Description |
|------|-------------|
| `Franchise` | Franchise history tracking |
| `Roster`, `RosterPlayer` | Team roster with physical attributes |
| `Standing` | Team standings (wins/losses/points) |
| `Boxscore`, `BoxscoreTeam` | Detailed boxscore with player stats |
| `PlayByPlay`, `PlayEvent` | 30+ event fields for play-by-play |
| `GameMatchup`, `MatchupTeam` | Game matchup/landing info |
| `GameStory`, `StoryTeam` | Game story view |
| `GameSummary`, `PeriodScoring` | Detailed game summaries |
| `GoalSummary`, `ShootoutAttempt` | Goal and shootout details |
| `ThreeStar` | Three stars selection |
| `ShiftChart`, `ShiftEntry` | Shift tracking |
| `PlayerLanding` | Full player profile |
| `PlayerStats`, `SeasonTotal` | Career/season stats |
| `SkaterStats`, `GoalieStats` | Position-specific stats |
| `GameLog`, `PlayerGameLog` | Game-by-game performance |

## Enums in nhl-api-go with NO GORM equivalent

- `Position` (C, LW, RW, D, G)
- `Handedness` (L, R)
- `GoalieDecision` (W, L, T, OTL)
- `PeriodType` (REG, OT, SO)
- `HomeRoad` (H, R)
- `ZoneCode` (O, D, N)
- `DefendingSide` (left, right)
- `GameScheduleState` (OK, PPD, SUSP, TBD, CNCL, etc.)
- `PlayEventType` (18 different event types)
- `GameType` (preseason, regular, playoff, allstar)

---

## Conclusion

**GORM types have everything needed** for the current Yahoo Fantasy Hockey use case - they're not missing any fields required for fantasy scoring. The Yahoo-specific fields (logo URLs, home links, AllStars flag) are correctly present only in GORM.

**nhl-api-go provides much richer data** that could enhance future features:
- Game summaries, three stars, shootout details
- Player rosters with physical attributes
- Standings tracking
- Play-by-play events
- Broadcasting information
- Venue/location data
