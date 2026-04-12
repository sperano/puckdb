# NHL Edge API Endpoints

Undocumented endpoints reverse-engineered from the NHL website. These power the
[NHL Edge](https://www.nhl.com/nhl-edge/) tracking stats (skating speed, distance,
shot speed, zone time, etc.).

**Base URL:** `https://api-web.nhle.com/`

**Parameters:**
- `{playerId}` / `{goalieId}` / `{teamId}` — NHL IDs
- `{season}` — e.g. `20252026`
- `{gameType}` — `2` = regular season, `3` = playoffs
- All detail endpoints also support a `/now` variant (e.g. `skater-detail/{playerId}/now`)

**Auth:** None required (unauthenticated), but these are unofficial and could change at any time.

## Skater Endpoints

### Detail

| Endpoint | Description |
|----------|-------------|
| `v1/edge/skater-detail/{playerId}/{season}/{gameType}` | Combined Edge stats |
| `v1/edge/skater-skating-speed-detail/{playerId}/{season}/{gameType}` | Top speed, avg speed per game |
| `v1/edge/skater-skating-distance-detail/{playerId}/{season}/{gameType}` | Distance skated per game |
| `v1/edge/skater-shot-speed-detail/{playerId}/{season}/{gameType}` | Shot velocity data |
| `v1/edge/skater-shot-location-detail/{playerId}/{season}/{gameType}` | Shot locations with Edge metrics |
| `v1/edge/skater-zone-time/{playerId}/{season}/{gameType}` | OZ/NZ/DZ time percentages |
| `v1/edge/skater-comparison` | Head-to-head Edge comparison |
| `v1/edge/skater-landing` | Edge landing page data |

### Leaderboards (Top 10)

| Endpoint | Description |
|----------|-------------|
| `v1/edge/skater-speed-top-10` | Fastest skaters |
| `v1/edge/skater-distance-top-10` | Most distance skated |
| `v1/edge/skater-shot-speed-top-10` | Hardest shots |
| `v1/edge/skater-shot-location-top-10` | Shot location leaders |
| `v1/edge/skater-zone-time-top-10` | Zone time leaders |

## Goalie Endpoints

### Detail

| Endpoint | Description |
|----------|-------------|
| `v1/edge/goalie-detail/{goalieId}/{season}/{gameType}` | Edge goalie stats |
| `v1/edge/goalie-5v5-detail/{goalieId}/{season}/{gameType}` | 5v5 save analytics |
| `v1/edge/goalie-shot-location-detail/{goalieId}/{season}/{gameType}` | Shot location saves |
| `v1/edge/goalie-save-percentage-detail` | Save % breakdown |
| `v1/edge/goalie-comparison` | Goalie head-to-head |

### Leaderboards (Top 10)

| Endpoint | Description |
|----------|-------------|
| `v1/edge/goalie-edge-save-pctg-top-10` | Save % leaders |
| `v1/edge/goalie-5v5-top-10` | 5v5 save leaders |
| `v1/edge/goalie-shot-location-top-10` | Shot location save leaders |

## Team Endpoints

### Detail

| Endpoint | Description |
|----------|-------------|
| `v1/edge/team-detail/{teamId}/{season}/{gameType}` | Team-level Edge stats |
| `v1/edge/team-skating-speed-detail/{teamId}/{season}/{gameType}` | Team skating speed |
| `v1/edge/team-skating-distance-detail/{teamId}/{season}/{gameType}` | Team distance |
| `v1/edge/team-shot-speed-detail/{teamId}/{season}/{gameType}` | Team shot speed |
| `v1/edge/team-shot-location-detail/{teamId}/{season}/{gameType}` | Team shot locations |
| `v1/edge/team-zone-time-details` | Team zone time |

### Leaderboards (Top 10)

| Endpoint | Description |
|----------|-------------|
| `v1/edge/team-skating-speed-top-10` | Fastest teams |
| `v1/edge/team-skating-distance-top-10` | Most distance skated |
| `v1/edge/team-shot-speed-top-10` | Hardest shooting teams |
| `v1/edge/team-shot-location-top-10` | Shot location leaders |
| `v1/edge/team-zone-time-top-10` | Zone time leaders |

## Categorical Endpoints

| Endpoint | Description |
|----------|-------------|
| `cat/edge/skater-detail` | Categorical skater Edge data |
| `cat/edge/goalie-detail` | Categorical goalie Edge data |

## References

- [Zmalski/NHL-API-Reference #69](https://github.com/Zmalski/NHL-API-Reference/issues/69) — initial discovery
- [dfleis/nhl-api-docs](https://github.com/dfleis/nhl-api-docs) — automated parsing of 500+ endpoints
- [coreyjs/nhl-api-py](https://github.com/coreyjs/nhl-api-py) — Python client with Edge support
