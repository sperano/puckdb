# NHL Edge API Endpoints

Undocumented endpoints reverse-engineered from the NHL website. These power the
[NHL Edge](https://www.nhl.com/nhl-edge/) tracking stats (skating speed, distance,
shot speed, zone time, etc.).

**Base URL:** `https://api-web.nhle.com/`

**Parameters:**
- `{playerId}` / `{goalieId}` / `{teamId}` — NHL IDs
- `{season}` — e.g. `20252026`
- `{gameType}` — `2` = regular season, `3` = playoffs
- All detail endpoints support a `/now` variant (e.g. `skater-detail/{playerId}/now`)
  which returns a 307 redirect to the current season

**Auth:** None required (unauthenticated), but these are unofficial and could change at any time.

**Data availability:** Edge tracking data is available from the **2021-2022 season onward**
(confirmed via `seasonsWithEdgeStats` in API responses). Earlier seasons return empty/404.

**Verified:** April 2026

## Working Endpoints (200 OK)

### Skater Detail

Parameterized by `{playerId}/{season}/{gameType}`:

| Endpoint | Description |
|----------|-------------|
| `v1/edge/skater-detail/{p}/{s}/{gt}` | Combined Edge stats (speed, distance, shots, zones) |
| `v1/edge/skater-skating-speed-detail/{p}/{s}/{gt}` | Top skating speeds per game |
| `v1/edge/skater-skating-distance-detail/{p}/{s}/{gt}` | Distance skated per game |
| `v1/edge/skater-shot-speed-detail/{p}/{s}/{gt}` | Hardest shots per game |
| `v1/edge/skater-shot-location-detail/{p}/{s}/{gt}` | Shots by rink area |
| `v1/edge/skater-zone-time/{p}/{s}/{gt}` | OZ/NZ/DZ time percentages |
| `v1/edge/skater-comparison/{p}/{s}/{gt}` | Rich composite for head-to-head |

### Goalie Detail

Parameterized by `{goalieId}/{season}/{gameType}`:

| Endpoint | Description |
|----------|-------------|
| `v1/edge/goalie-detail/{g}/{s}/{gt}` | Combined Edge goalie stats |
| `v1/edge/goalie-5v5-detail/{g}/{s}/{gt}` | 5v5 save % per game |
| `v1/edge/goalie-shot-location-detail/{g}/{s}/{gt}` | Saves by rink area |
| `v1/edge/goalie-save-percentage-detail/{g}/{s}/{gt}` | Overall save % per game |
| `v1/edge/goalie-comparison/{g}/{s}/{gt}` | Rich composite (shotLocation, savePctg5v5, savePctg) |

### Team Detail

Parameterized by `{teamId}/{season}/{gameType}`:

| Endpoint | Description |
|----------|-------------|
| `v1/edge/team-detail/{t}/{s}/{gt}` | Combined team Edge stats |
| `v1/edge/team-skating-speed-detail/{t}/{s}/{gt}` | Top skating speeds (per player) |
| `v1/edge/team-skating-distance-detail/{t}/{s}/{gt}` | Distance per game (team total) |
| `v1/edge/team-shot-speed-detail/{t}/{s}/{gt}` | Team shot speed |
| `v1/edge/team-shot-location-detail/{t}/{s}/{gt}` | Team shots by area |
| `v1/edge/team-zone-time-details/{t}/{s}/{gt}` | Zone time by strength (all/es/pp/pk) + shot differential |
| `v1/edge/team-comparison/{t}/{s}/{gt}` | Rich composite (speed, distance, shotLocation, zoneTime, shotDifferential) |

### Landing Pages (League-Wide Leaders)

Parameterized by `{season}/{gameType}` only:

| Endpoint | Description |
|----------|-------------|
| `v1/edge/skater-landing/{s}/{gt}` | League leaders: hardest shot, fastest skater, etc. |
| `v1/edge/goalie-landing/{s}/{gt}` | League leaders: high-danger save%, etc. |
| `v1/edge/team-landing/{s}/{gt}` | League leaders: shot attempts over 90, etc. |

### CAT (Catch All Tracking)

Parameterized by `{playerId}/{season}/{gameType}`:

| Endpoint | Description |
|----------|-------------|
| `v1/cat/edge/skater-detail/{p}/{s}/{gt}` | Same as skater-detail minus `distanceMaxGame` |
| `v1/cat/edge/goalie-detail/{g}/{s}/{gt}` | Same as goalie-detail |

## Dead Endpoints (404/500)

All `*-top-10` leaderboard endpoints are dead. Some return 500 instead of 404.
The landing pages serve as the replacement (they return league-wide leaders).

- `v1/edge/skater-speed-top-10` — 404
- `v1/edge/skater-distance-top-10` — 404
- `v1/edge/skater-shot-speed-top-10` — 404
- `v1/edge/skater-shot-location-top-10` — 404
- `v1/edge/skater-zone-time-top-10` — 500
- `v1/edge/goalie-edge-save-pctg-top-10` — 404
- `v1/edge/goalie-5v5-top-10` — 404
- `v1/edge/goalie-shot-location-top-10` — 404
- `v1/edge/team-skating-speed-top-10` — 404
- `v1/edge/team-skating-distance-top-10` — 500
- `v1/edge/team-shot-speed-top-10` — 404
- `v1/edge/team-shot-location-top-10` — 404
- `v1/edge/team-zone-time-top-10` — 500
- `v1/edge/by-the-numbers` — 404

## puckdb Integration

### Data stored in DB (from detail endpoints)

| Source Endpoint | DB Tables |
|----------------|-----------|
| `skater-detail` | `edge_skater_stats`, `edge_skater_shot_locations`, `edge_skater_sog_summary` |
| `goalie-detail` | `edge_goalie_stats`, `edge_goalie_shot_location_summary`, `edge_goalie_shot_locations` |
| `team-detail` | `edge_team_stats`, `edge_team_sog_summary`, `edge_team_shot_locations` |
| `team-zone-time-details` | `edge_team_zone_time_by_strength`, `edge_team_shot_differential` |

### Data cached only (filesystem)

All other endpoints (sub-detail, comparison, landing, CAT) are fetched and cached
on the filesystem but not imported to the database. Their data overlaps with the
detail endpoints or is derivable from stored data.

### Temporal Workflows

- `FetchEdgeSeasonsWorkflow` / `FetchEdgeWorkflow` — download Edge data to cache
- `ImportEdgeSeasonsWorkflow` / `ImportEdgeWorkflow` — import cached data to DB

### GraphQL

Queries: `edgeSkaterStats`, `edgeGoalieStats`, `edgeTeamStats`
Mutations: `fetchEdgeStats`, `importEdgeStats` (with `cancel*` variants)

### MCP Tools

`get_edge_skater_stats`, `get_edge_goalie_stats`, `get_edge_team_stats`

## References

- [Zmalski/NHL-API-Reference #69](https://github.com/Zmalski/NHL-API-Reference/issues/69) — initial Edge endpoint discovery thread (borderline maintained, last commit Nov 2025)
- [dfleis/nhl-api-docs](https://github.com/dfleis/nhl-api-docs) — parsed NHL WADL file with 66 Edge endpoint variants (unmaintained, last commit Sep 2025)
- [coreyjs/nhl-api-py](https://github.com/coreyjs/nhl-api-py) — **active** Python client with 28 Edge methods (Apache 2.0, last commit Mar 2026)
