# MCP server: proposed tools

Status: proposal only. Nothing below is implemented. The goal is to pick what
to build next for `puckdb mcp-server` (`internal/mcpserver/`).

## Where the server stands

- `nhl` toolset: 47 tools covering players, games, box scores, game logs,
  season and club totals, standings, playoffs, Edge, play-by-play.
- `yahoo` toolset: 8 tools: leagues, teams, one team's roster on a date, roto
  standings, all matchups, draft results, unrostered skaters/goalies. Every
  league-scoped tool goes through `leagueGuard.scoped`.
- Nothing reads the draft helper (rankings, projections, news events,
  adjustments, live draft sessions). Large parts of the Yahoo data are also
  unread: league settings, transactions, H2H standings, rosters of the whole
  league, the draftable pool, managers.

The idea behind this list comes from the open `get_draft_rankings` request. To
get a league's pre-draft ranking, an agent had to read
`draft_ranking_players` through a Postgres MCP (about 12 KB of JSONB per player,
about 10 MB per snapshot). The workaround was a `COPY` run through `kubectl
exec`. That request also asked whether the Yahoo tools should move to their own
server. The `--mcp-toolsets nhl|yahoo` split and `--mcp-yahoo-leagues` already
answer that, so new Yahoo and draft tools go into the `yahoo` toolset (or a new
toolset, see open questions).

## Ground rules for the new tools

1. **Read-only.** The server is documented as curated and read-only. Some
   service paths write: `draftboard.Service.Board` stores a
   `draft_recommendation_runs` row on every call; overrides are mutations.
   Those are listed separately and need a decision first.
2. **Go through the existing services, not new SQL, where a service exists.**
   Examples are `draftrank.Service`, `newsevent.LoadEventDigest`,
   `news.EvaluateCoverage` and `draft.LoadLeagueReport`. That way, MCP, GraphQL
   and the CLI return the same values. `mcpserver.NewServer` takes only
   `*sqlcdb.Queries` today, so `cmd/mcp_server.go` would have to pass the
   services in, for example in `Options`.
3. **League guard for every league-scoped tool.** `leagueGuard.scoped` reads a
   numeric `league_id`. The draft tools are keyed by league key + season, so
   the guard needs a key-based variant (`scopedKey`) that checks
   `--mcp-yahoo-leagues` directly. `TestYahooToolsAreLeagueGuarded` must cover
   it.
4. **Bounded, compact output.** Every list takes `limit`, with a named default
   and maximum. Rows are CSV (`ResultCSV`). Metadata goes in a header printed
   once, not repeated on each row. No raw JSONB.
5. **Prefer an optional argument over a new tool.** The server already
   registers 55 tools, and clients load every tool description into the
   model's context. Where an existing tool covers the same rows, the proposal
   adds arguments to it instead of adding a tool.
6. **No personal data.** `yahoo_team_managers` has `email` and `guid` columns.
   Any manager tool returns only an explicit column list.

## Priority 1: what agents cannot do today

| Tool | Toolset | Inputs | Returns | Backed by |
|------|---------|--------|---------|-----------|
| `get_draft_rankings` | yahoo | `league` (key or ID), `season`, `snapshot_id`, `scenario`, `positions`, `search`, `sort`, `offset`, `limit` (default covers a normal pool, e.g. 1000, up to a named max) | Header (snapshot id, as-of, scenario, total, league name/format/categories/roster slots, `provisional`, `rulesSource`, issues). Then one row per player: rank, name, team, positions, position rank, tier, value, uncertainty, rank change vs. baseline, status/injury note, projected category totals by abbreviation | `draftrank.Service.Rankings`, the same path as GraphQL `draftRankings` and `puckdb draft rankings`; reuse the draftrank CSV export if it fits. Target: 838 players well under 100 KB |
| `get_draft_player_detail` | yahoo | `league`, `season`, `snapshot_id`, `player_keys` (max ~10) | Every scenario placement with contributions and explanations, for a few players | `draftrank` comparison view (GraphQL `draftPlayerComparison`) |
| `get_yahoo_league_settings` | yahoo | `league_id` | Scoring categories (name, abbreviation, sort order, points weight, display-only), roster slots, plus a summary of the latest rules snapshot (format, source, stand-in flag) | `GetYahooLeagueStatCategories`, `GetYahooLeagueRosterPositions`, `GetLatestYahooLeagueRuleSnapshot`. Without this, an agent cannot tell what a league scores |
| `get_yahoo_transactions` | yahoo | `league_id`, `type` (add/drop/trade/...), `since`, `limit` | Transactions, newest first, flattened to one row per player moved (player, from/to team, type, time, status) | `yahoo_transactions` + `yahoo_transaction_players`. Needs a new query: `GetYahooTransactionsByLeague` has no limit or date filter |
| `get_player_news` | news (see open questions) | `player_id` or `yahoo_player_id`, `since`, `limit`, `include_inactive` | Validated news events (type, report status, effective date, duration, lifecycle) with short attributed evidence quotes | `newsevent.LoadEventDigest` / `ListNewsEventsForPlayer` + `ListNewsEventEvidence` |
| `get_recent_form` | nhl | `season`, `group` (skaters/goalies), `as_of` (default today), `days` (default 30), `position`, `limit` | Per-player totals over the window | The `skater_recent_stats` / `goalie_recent_stats` views are fixed to `CURRENT_DATE - 30 days`, so this needs a new parameterized query. The same query should back the unrostered tools (see fixes) |
| `get_team_schedule` | nhl | `start_date`, `end_date`, `team_id` (optional) | Games per team in the range, split into back-to-backs and light/heavy nights (nights with few or many games) | New aggregate over `games`; `GetGamesByDateRange` lists the games but does not count them. This is the main fantasy question for weekly streaming |

The NHL ↔ Yahoo bridge needs one more piece, and it can be an argument rather
than a tool: `get_player` and `search_player` should accept `yahoo_id`
(`GetPlayerByYahooID`). The Yahoo tools return Yahoo player IDs, and today an
agent cannot turn one into an NHL ID except by searching the name.

## Priority 2: useful, mostly backed by existing queries

### Yahoo leagues

| Tool or change | Inputs | Backed by |
|----------------|--------|-----------|
| `get_yahoo_league_rosters` | `league_id`, `date` | `GetYahooTeamRostersByDate` joined to players: every team's roster in one call instead of one call per team |
| `get_yahoo_player_ownership` | `league_id`, `yahoo_player_id` | `GetYahooTeamRostersByPlayer`: which team held the player on which dates |
| `get_yahoo_team_standings` | `league_id`, `date` (default latest), `team_id` | `GetYahooTeamSummariesByDate` / `ByTeam` + summary stats: H2H records and per-category values. `get_yahoo_roto_standings` covers roto only |
| `get_yahoo_matchups`: add `week`, `team_id` | | `GetYahooMatchupsByWeek`, `GetYahooMatchupsByTeam` |
| `get_yahoo_teams_by_league`: add the managers (nickname, commissioner, current login only). Waiver priority and the owner flag are already in its rows | | `GetYahooTeamManagers` (explicit columns) |
| `get_yahoo_league_players` | `league_id`, `positions`, `status`, `unmatched_only`, `limit` | `ListYahooLeaguePlayersWithNHL`: the draftable pool with eligibility, status and injury note, plus whether each player matched an NHL player |
| `get_yahoo_season_team_totals` | `league_id` | `GetYahooSeasonTeamTotals` (view `yahoo_season_team_totals`) |

### Draft helper

| Tool | Inputs | Backed by |
|------|--------|-----------|
| `get_draft_leagues` | `season` | `draftrank.Service.Leagues`: each league's ranking state, latest refresh, issues. This is where an agent starts before calling `get_draft_rankings` |
| `get_draft_overrides` | `league`, `player_key`, `include_inactive` | `draftrank.Service.Overrides` (read only; creating and resetting stay in GraphQL) |
| `get_draft_rules_report` | `season`, `leagues` | `draft.LoadLeagueReport` + `draft.WriteComparison`: the `puckdb draft rules` Markdown, warnings included |
| `get_draft_pool_coverage` | `season`, `league` | `draft.Coverage` / `WritePoolReport` (`puckdb draft pool`) |

### News

| Tool | Inputs | Backed by |
|------|--------|-----------|
| `get_recent_news_events` | `since`, `event_types`, `limit` | `ListRecentNewsEvents` (+ evidence): what changed across the league today |
| `get_news_coverage` | `season` | `news.EvaluateCoverage`: which sources are fresh, failing, stale or missing, so an agent knows whether "no news" is real |

### NHL

| Tool or change | Inputs | Backed by |
|----------------|--------|-----------|
| `get_game_summary` | `game_id` | One call for game facts no tool returns today: goals with scorer/time/clip URL (`GetGoalHighlights`), shootout attempts (`GetShootoutAttempts`), officials, coaches and scratches (`GetGameOfficials`, `GetGameCoaches`, `GetGameScratches`), plus the three stars |
| `get_player_linemates` | `player_id`, `season` (or date range), `limit` | `even_strength_pair_toi` + `even_strength_skater_games`: shared even-strength TOI per teammate. Only the projection model reads this data today; a new query is needed. Useful for "who is he playing with" |
| `get_season_roster` | `team_id`, `season` | `GetSeasonRosterByTeam`; `get_players_by_team` returns only the current assignment |
| `get_skater_season_stats` / `get_goalie_season_stats`: add `sort_by` and `position` | | New query with a whitelisted ORDER BY (sqlc cannot bind a column name): leaders by goals, PPP, hits, blocks, SOG, save percentage, ... |
| `get_skater_game_log` / `get_goalie_game_log`: add `start_date` / `end_date` | | `GetSkaterStatsByPlayerAndDateRange`, `GetGoalieStatsByPlayerAndDateRange` |
| `get_edge_leaders` | `season`, `metric`, `group` (skater/goalie/team), `limit` | `GetEdge*StatsBySeason`. They sort by one fixed column, so a whitelisted sort is needed, as above |
| `get_awards` | `season` or `trophy` | `GetAwardsBySeason`, `GetAwardsByTrophy`; `get_player_awards` is per player only |
| `get_team_season_summary` | `team_id`, `season` | `GetTeamGoalsPerGame`, `GetTeamSkaterSeasonTotals`, `GetTeamGoalieSeasonTotals` |

## Priority 3: needs a decision, or has little value

- **Live draft board (`get_draft_board`, `get_draft_session_events`,
  `get_draft_shortlist`).** This has the most value on draft day: "who should
  I take now" with roster fit. But `draftboard.Service.Board` persists a
  recommendation run on every call, so it is not read-only. Possible ways out:
  a non-persisting recommendation path, or accept these writes (an audit
  trail) explicitly. `Events` and the shortlist are plain reads.
- **News adjustment explanation (`get_news_adjustment`)**: why news moved a
  player in a league's ranking (`ListNewsAdjustmentPlayers`,
  `ListNewsAdjustmentEvents` of the snapshot's run). It is mostly covered by
  `get_draft_player_detail`; add it only if that is not enough.
- **`get_head_to_head`** (two teams, optional season): it needs a new query.
  `list_games` with `team_id` gets close.
- **`get_franchise_history`** (`GetTeamHistory`, `GetFranchise`): relocations and
  names per season.
- **Pool simulator toolset (`sim`)**: `ListSimPools`, standings, transactions,
  agent turns. It is for debugging simulations, not for answering hockey
  questions, so it belongs in its own toolset if it is added at all.
- **Shifts**: `shifts` (~14.5M rows) has no read query. Per-game TOI is already
  in the box score, and line data is better served by `get_player_linemates`.
  Skip it.

## Fixes to existing tools found while surveying

These are not new calls, but they affect the answers agents get now.

- **The Edge tools probably return nothing.** `get_edge_skater_stats`,
  `get_edge_goalie_stats` and `get_edge_team_stats` describe `season` as the
  start year ("2024 for 2024-2025") and pass it to the query unchanged. The
  import stores `season.ID()` (`internal/worker/nhl/import_edge_cached.go`),
  that is `20242025` (`startYear*10000 + endYear`), which migration `000003`
  also assumes. I found this by reading the code; I did not check it against
  a database. The tools should
  take `20242025` like every other tool, or accept both forms.
- **The unrostered tools mix two dates.** `get_unrostered_skaters` and
  `get_unrostered_goalies` take the roster as of `date`, but their stats come
  from views fixed to the last 30 days before *today*. A past date pairs that
  day's rosters with today's form, and off-season the result is empty. Back
  them with the `get_recent_form` query, using a window that ends at `date`.
- **Unbounded results.** `get_games_by_team` (every season), `get_standings_by_season`
  (one row per team per day), `get_active_players` and `get_players_by_position`
  have no `limit`. They can return thousands of rows into an agent's context.
  Each should take a `limit` with the default `defaultResultLimit`, or require
  a season.

## Open questions for the owner

1. Where should news tools go? The events concern NHL players (public), but
   they quote third-party articles and feed league rankings. Options: the `nhl`
   toolset, the `yahoo` toolset, or a new `news` toolset.
2. The draft tools (rankings, rules, pool, overrides): keep them in `yahoo`,
   or add a `draft` toolset so a draft-day client gets a short tool list?
3. The live board: build a non-persisting recommendation path, or accept the
   recommendation-run writes from MCP?
4. Should the Edge season fix and the unrostered fix ship before new tools?
   They are bugs in tools agents already call.
