# PuckDB MCP Server Plan

## Overview

Build a dedicated MCP server (`puckdb mcp-server`) that exposes curated, read-only sqlc queries as MCP tools for Maurice. Replaces the current approach of giving the LLM generic SQL access via postgres-mcp.

## Architecture

```
Maurice REPL (local)
    └─ MultiClient (merges tools, routes by name)
          ├─ puckdb-mcp (NEW, ~45 curated hockey tools, runs in cluster)
          └─ postgres-mcp (existing, ONLY pg_read_query exposed, raw SQL fallback)
```

## Configuration

Maurice loads MCP servers from `~/.puckdb/maurice.yaml` (override with `--maurice-config`):

```yaml
mcp_servers:
  - name: puckdb
    url: https://puckdb-mcp.***REMOVED***/mcp
  - name: postgres
    url: https://postgres-mcp.***REMOVED***/mcp
    tools: [pg_read_query]  # whitelist — only expose these tools
```

## Design Decisions

- **No in-process MCP** — Maurice has no direct DB access; both MCP servers run in the cluster
- **postgres-mcp filtered to `pg_read_query` only** — the other 200+ tools are DBA tools that waste context window
- **No tool name prefixing** — puckdb tools use hockey names (`search_player`), postgres-mcp uses `pg_*`, no collision
- **JSON for single entities (`:one`), CSV for tabular data (`:many`)** — CSV is more token-efficient for stat tables
- **pgtype unwrapping** — formatter must handle `pgtype.Text`, `pgtype.Int8`, `pgtype.Date`, etc.
- **Helm/deploy deferred** — Go code first, cluster deployment later

## File Structure

### New Files

| File | Purpose |
|------|---------|
| `maurice/config.go` | Parse `maurice.yaml`, MCP server list with optional tool whitelist |
| `mcp/multi_client.go` | Unions tools from multiple MCP clients, routes calls by name |
| `mcpserver/server.go` | MCP server constructor + `RegisterAll` |
| `mcpserver/format.go` | JSON/CSV result formatters, pgtype unwrapping |
| `mcpserver/tools_resolve.go` | Resolution tools (search/find by name) |
| `mcpserver/tools_players.go` | Player detail/stats tools |
| `mcpserver/tools_games.go` | Game/schedule tools |
| `mcpserver/tools_stats.go` | Skater/goalie stats tools |
| `mcpserver/tools_standings.go` | Standings tools |
| `mcpserver/tools_fantasy.go` | Fantasy analysis tools |
| `mcpserver/server_test.go` | Tests |
| `cmd/mcp_server.go` | `puckdb mcp-server` subcommand (postgres flags + HTTP serve) |

### Modified Files

| File | Change |
|------|--------|
| `cmd/maurice.go` | Load config, create MultiClient from server list |
| `cmd/root.go` | Register `mcp-server` subcommand |
| `config/flags.go` | Replace `maurice-mcp-url` with `maurice-config` flag |
| `config/defaults.go` | Default config path (`~/.puckdb/maurice.yaml`) |

## Tool Inventory (~45 tools)

### Resolution (6)

| Tool | sqlc Query | Format | Purpose |
|------|-----------|--------|---------|
| `search_player` | `SearchPlayersByName` | CSV | Search by name (accent-insensitive) |
| `find_team` | `GetTeamIDByAbbrev` | JSON | Resolve team abbrev → ID for a season |
| `list_teams` | `GetSeasonTeams` | CSV | All teams for a season |
| `list_team_abbreviations` | `GetSeasonTeamAbbrevs` | CSV | Quick abbrev lookup |
| `list_seasons` | `GetAllSeasons` | CSV | Available seasons |
| `list_franchises` | `GetAllFranchises` | CSV | All franchises |

### Players (8)

| Tool | sqlc Query | Format |
|------|-----------|--------|
| `get_player` | `GetPlayer` | JSON |
| `get_players_by_team` | `GetPlayersByTeam` | CSV |
| `get_players_by_position` | `GetPlayersByPosition` | CSV |
| `get_active_players` | `GetActivePlayers` | CSV |
| `get_player_career_totals` | `GetPlayerNHLSeasonTotals` | CSV |
| `get_player_awards` | `GetPlayerAwards` | CSV |
| `get_player_roster_history` | `GetSeasonRosterByPlayer` | CSV |
| `get_player_three_stars` | `GetPlayerThreeStarSelections` | CSV |

### Games (7)

| Tool | sqlc Query | Format |
|------|-----------|--------|
| `get_game` | `GetGame` | JSON |
| `get_games_by_date` | `GetGamesByDate` | CSV |
| `get_games_by_season` | `GetGamesBySeason` | CSV |
| `get_games_by_team` | `GetGamesByTeam` | CSV |
| `get_games_by_team_and_season` | `GetGamesByTeamAndSeason` | CSV |
| `get_game_three_stars` | `GetGameThreeStars` | CSV |
| `get_game_broadcasts` | `GetGameBroadcasts` | CSV |

### Stats (10)

| Tool | sqlc Query | Format |
|------|-----------|--------|
| `get_game_skater_stats` | `GetGameSkaterStatsByGame` | CSV |
| `get_game_goalie_stats` | `GetGameGoalieStatsByGame` | CSV |
| `get_game_skater_stats_by_team` | `GetGameSkaterStatsByGameAndTeam` | CSV |
| `get_game_goalie_stats_by_team` | `GetGameGoalieStatsByGameAndTeam` | CSV |
| `get_skater_season_totals` | `GetSkaterSeasonTotals` | JSON |
| `get_goalie_season_totals` | `GetGoalieSeasonTotals` | JSON |
| `get_skater_game_log` | `GetSkaterStatsByPlayerAndSeason` | CSV |
| `get_goalie_game_log` | `GetGoalieStatsByPlayerAndSeason` | CSV |
| `get_club_skater_stats` | `GetClubSkaterStatsByTeam` | CSV |
| `get_club_goalie_stats` | `GetClubGoalieStatsByTeam` | CSV |

### Standings (4)

| Tool | sqlc Query | Format |
|------|-----------|--------|
| `get_standings_by_date` | `GetStandingsSnapshotsByDate` | CSV |
| `get_standings_by_season` | `GetStandingsSnapshotsBySeason` | CSV |
| `get_standings_by_season_and_date` | `GetStandingsSnapshotsBySeasonAndDate` | CSV |
| `get_standings_by_team` | `GetStandingsSnapshotsByTeam` | CSV |

### Fantasy (10)

| Tool | sqlc Query | Format |
|------|-----------|--------|
| `get_yahoo_leagues` | `GetAllYahooLeagues` | CSV |
| `get_yahoo_teams_by_league` | `GetYahooTeamsByLeague` | CSV |
| `get_yahoo_roster` | `GetYahooRosterWithPlayers` | CSV |
| `get_yahoo_roto_standings` | `GetYahooRotoStandings` | CSV |
| `get_skater_season_stats` | `GetSkaterSeasonStats` | CSV |
| `get_goalie_season_stats` | `GetGoalieSeasonStats` | CSV |
| `get_unrostered_skaters` | `GetUnrosteredSkaters` | CSV |
| `get_unrostered_goalies` | `GetUnrosteredGoalies` | CSV |
| `get_yahoo_matchups` | `GetYahooMatchupsByLeague` | CSV |
| `get_yahoo_draft_results` | `GetYahooDraftResultsByLeague` | CSV |

## Phases

### Phase 1: Infrastructure ✅
- [x] `maurice/config.go` — parse `maurice.yaml`
- [x] `mcp/multi_client.go` — MultiClient with tool whitelisting
- [x] `mcp/client.go` — added NoopClient for empty config
- [x] `mcpserver/server.go` — server skeleton with tool registration
- [x] `mcpserver/format.go` — JSON/CSV formatters with pgtype unwrapping
- [x] `mcpserver/tools_resolve.go` — 5 resolution tools (search_player, find_team, list_teams, list_seasons, list_franchises)
- [x] `cmd/mcp_server.go` — `puckdb mcp-server` command (stdio + HTTP)
- [x] `cmd/maurice.go` — load config, use MultiClient via `buildMCPClient()`
- [x] `cmd/api.go` — updated to use config-based MCP client
- [x] `config/flags.go` — replaced `maurice-mcp-url` with `maurice-config`
- [x] `config/defaults.go` — updated defaults

### Phase 2: Player Tools ✅
- [x] `mcpserver/tools_resolve.go` — 5 resolution tools (moved to Phase 1)
- [x] `mcpserver/tools_players.go` — 8 player tools
- [x] `mcpserver/format.go` — added nullable enum handling (Null* structs)
- [ ] Tests

### Phase 3: Game + Stats Tools ← CURRENT
- [ ] `mcpserver/tools_games.go` — 7 game tools
- [ ] `mcpserver/tools_stats.go` — 10 stats tools
- [ ] Tests

### Phase 4: Standings + Fantasy Tools
- [ ] `mcpserver/tools_standings.go` — 4 standings tools
- [ ] `mcpserver/tools_fantasy.go` — 10 fantasy tools
- [ ] Tests

### Phase 5: Deploy
- [ ] Helm service entry for puckdb-mcp
- [ ] Container config (same image, new entrypoint)
- [ ] DNS/ingress for puckdb-mcp.***REMOVED***
