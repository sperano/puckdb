# PuckDB

Go application that imports NHL hockey data and Yahoo Fantasy Sports data. Downloads from NHL API and Yahoo API, caches locally as files, stores in PostgreSQL. Temporal orchestrates long-running workflows.

## Quick Reference

### Build
```bash
cd /Users/eric/code/puckdb-project/ws/puckdb && go build -o /tmp/puckdb .
```
> **Note:** Always `cd` into the module directory before `go build`. The `-C` flag doesn't work reliably outside a module context.

### Run
```bash
/tmp/puckdb api --instance puckdb    # GraphQL server (port 8787); --instance is required
/tmp/puckdb worker                   # Temporal worker (port 8788)
/tmp/puckdb metrics                  # Prometheus exporter for cache/Redis/DB
```

### Test
```bash
go test ./...                        # All tests
go test -cover ./...                 # With coverage
```

### Generate Code
```bash
go tool gqlgen generate   # GraphQL (from puckdb root dir; gqlgen is pinned via the go.mod tool directive)
```

**GraphQL code generation:**
1. Edit `internal/graph/schema.graphqls` (schema, types, queries, mutations)
2. Run `go tool gqlgen generate` from puckdb directory
3. Generated files: `internal/graph/generated/generated.go`, `internal/graph/model/models_gen.go`
4. New resolvers appear as `panic("not implemented")` stubs in `internal/graph/schema.resolvers.go`
5. Implement resolver logic in `internal/graph/resolver.go` (private methods like `fetchPlayerLandings`)
6. Update `schema.resolvers.go` stubs to call the new `resolver.go` methods

## CLI Commands

| Command | Description |
|---------|-------------|
| `api` | HTTP server: GraphQL at `/graphql`, playground at `/graphql/`, OAuth at `/yahoo/*` |
| `worker` | Temporal worker for download/import workflows, metrics at `/metrics` |
| `sync` | Sync data into the database |
| `metrics` | Expose cache, Redis, and database metrics as Prometheus metrics |
| `mcp-server` | MCP server exposing curated read-only data tools; HTTP on `--mcp-port` (default 8790) or `--mcp-stdio`; `--mcp-toolsets` picks `nhl` (default), `yahoo`, `news` or a comma-separated combination (server name `puckdb-<toolsets>`); `--mcp-yahoo-leagues` limits the `yahoo` toolset to listed league keys (empty: every league); with the `yahoo` toolset, a listed key matching no `yahoo_leagues` row fails startup |
| `db init` | Create tables, seed NHL data |
| `db drop` | Drop all tables |
| `db migrate` | Run database migrations directly |
| `db force-version <version>` | Clear a dirty migration state after inspecting the schema (see https://wiki.spe.quebec/en/puckdb/migration-recovery) |
| `db check-teams` | Report regular-season/playoff games whose team has no `season_teams` row (such games vanish from games queries) |
| `db provision` | Create database/user on shared PostgreSQL |
| `draft rules` | Markdown comparison of leagues' imported rules (scoring, roster, draft, settings) with warnings; `--draft-season`, `--draft-leagues`, `--draft-output` |
| `draft pool` | Coverage of leagues' draftable player pools (eligibility gaps, unmatched NHL players) |
| `draft rankings` | A league's stored ranking snapshot as a table, CSV or JSON (`--draft-league`: full key, or numeric ID in `--draft-season`, default the current season; `--draft-positions`, `--draft-format`, search/sort/pagination flags); same service and values as GraphQL (see wiki `puckdb/draft/rankings-api.md`) |
| `sync draft` | Force-refresh and reconcile one full-key Yahoo draft session (`--draft-league`: full key, or numeric ID in `--draft-season`, default the season whose rules for that ID were imported last; `--league` is a deprecated alias); `--watch` polls with bounded backoff and final reconciliation (see wiki `puckdb/yahoo-draft-watch.md`) |
| `draft session` | Inspect live draft state, capability observations, and perform local add/correct/undo/conflict resolution without submitting Yahoo picks; league flags and resolution as `sync draft` (see wiki `puckdb/draft/league-references.md`) |
| `api` → `/draft/` | Maurice live draft board: versioned polling, watch/recovery controls, roster-fit recommendations, shortlist and pick history |
| `news report` | Markdown report of player news: source coverage (fresh/failing/stale/missing), incident candidates with attributed evidence, unattached story subjects; `--news-player-nhl-id`/`--news-player-yahoo-id` for one player (see https://wiki.spe.quebec/en/puckdb/draft/player-news) |
| `news events` | Markdown report of validated news events (evidence quotes, lifecycle history) and the extraction review queue (see https://wiki.spe.quebec/en/puckdb/draft/news-events) |
| `news eval` | Run the labeled news-event corpus through `--news-extract-provider`/`--news-extract-model`, print accuracy and unsupported-claim rate against the release thresholds, record the run (the gate for automatic effects) |
| `redis flush` | Flush a Redis database |
| `yahoo signout` | Clear OAuth2 token from Redis |
| `yahoo check-access` | Check whether the Yahoo API serves a season's leagues (AUTHORIZED / NOT AUTHORIZED / ERROR), optionally emailing the report over SMTP; meant for a daily CronJob (see https://wiki.spe.quebec/en/puckdb/yahoo-access-check) |
| `maurice` | Interactive AI hockey chat REPL |
| `user link-team` / `user unlink-team` | Link or unlink a Maurice user to a Yahoo team by hand (`internal/yahoolink`), for a user whose Yahoo email differs from their login email: links the team's Yahoo account (guid, so every season's teams of it) when every manager resolves to one, else the team alone; unlinking removes the team link and those account links; both clear the user's own deselection of the team (team picker), unlink only once neither the login email nor the member Yahoo email matches one of the team's managers or resolves to a Yahoo account (guid) one of them resolves to; `--instance`, `<username> <league id> <team id>` |
| `user list-teams` | List one user's or every user's linked Yahoo teams on `--instance` |
| `user reset-team-offer` | Clear one user's (`<username>`) or, with `--all-users`, every user's record of having answered the Maurice team picker's first-login offer on `--instance` (`app_users.team_offer_seen_at`), so it opens again at their next visit; links and deselections stay. Runs through the API's `@admin` `resetTeamOffer`/`resetInstanceTeamOffers` mutations (`--api-server-addr`, `--admin-token` or `--api-user`/`--api-password`), never opening the database |
| `sim` | Hockey pool simulator, all through GraphQL: `create --config <yaml>`, `cancel <pool-id>`, `status <pool-id>`, `tail` (turns and tool calls), `draft <pool-id>`; how far a pool runs is set in its config (`stopAfter`, `maxSeasonDays`, `startDate`/`endDate`), a running pool is never stepped or paused by hand (see wiki `puckdb/simulation.md`) |

## Package Structure

Only `main.go` and `cmd/` live at the module root; every library package sits under `internal/`, which the Go toolchain prevents other modules from importing.

Storage naming (see wiki `puckdb/plans/draft-storage-naming.md`): a concrete PostgreSQL type is `PGStore` (`NewPGStore(pool)`); an interface lives with its consumer and is named for its role (`SessionStore`, `RunStore`, `SessionSyncer`, `RankingReader`); "Snapshot" names only a stored, immutable version with a `*_snapshots` table (`draft.RulesSnapshot`, `draftrank.Snapshot`, `projection.Snapshot`), so an in-memory result or a poll gets another name (`draft.Ranking`, `draftsession.ObservedBoard`).

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI commands (Cobra + Viper) |
| `internal/worker/` | Temporal workflows and activities, split into `workflow/` (top-level workflows), `nhl/`, `yahoo/`, `player/`, `newsfeed/` (player news refresh), `draftranking/` (draft ranking refresh), `asset/` (image asset downloads: headshots, hero images, logos, on the `puckdb-asset-tasks` queue), `simulation/` (the hockey pool simulator, below), `admin/`, `shared/` |
| `internal/worker/simulation/` | Hockey pool simulator: `SimPoolWorkflow` and its activities run a rotisserie pool of LLM-managed teams over a past regular season (team names, snake draft, daily waivers/roster turns/scoring/standings), with the roto scoring engine, tool validators, fuzzy tool-call recovery, prompts and pricing; serialized names pinned in `registration.go`. Rules, lifecycle, idempotency and cost cap on wiki `puckdb/simulation.md`, replay contract on `puckdb/simulation-replay.md`, query groups on `puckdb/simulation-query-groups.md` |
| `internal/graph/` | GraphQL resolvers and schema (gqlgen) |
| `internal/database/` | PostgreSQL connection (pgx), migrations |
| `internal/sqlcdb/` | sqlc-generated type-safe queries (`queries/*.sql`, `sqlc generate`); `draftwatch`, `draftboard` and `draftrecommend` query only through it (`TestDraftStoresQueryOnlyThroughSqlc`) |
| `internal/cache/` | File-based caching, XML/JSON parsing |
| `internal/store/` | Storage backends (filesystem, in-memory, instrumented) for raw cached files |
| `internal/resource/` | Typed resource definitions (NHL/Yahoo paths, URLs, parse/format) |
| `internal/core/` | Shared primitives: file types, data origins, time helpers, resource interfaces |
| `internal/pgconv/` | pgtype conversions shared by GraphQL, workers, simulation and MCP: optional Go values to nullable columns (`PtrTo*`), NULL-as-empty display strings (`FormatDate`, `FormatInt4`, `FormatInt8`, `FormatPlayerPosition`), strict client `YYYY-MM-DD` parsing (`ParseDate`; never `pgtype.Date.Scan`, which takes `infinity` and rolls 02-30 into March), float64 ↔ NUMERIC at six fractional digits. Only conversions identical at every caller; API-shape mappings (GraphQL pointers, MCP float text, error wording) stay with the caller (see https://wiki.spe.quebec/en/puckdb/pgconv) |
| `internal/canonhash/` | Byte-level mechanism of every stored content identity: `Bytes` (hex SHA-256), `JSON` (of the `encoding/json` encoding), `Versioned` (`<version>:` + `JSON`); marshal errors are returned, never panicked. Canonical shape (identity struct, sorting, omitempty, prefix) stays with each caller, and each stored identity has a golden test in its caller's package (see wiki `puckdb/canonical-hashing.md`) |
| `internal/config/` | Flags, defaults, seasons YAML parsing |
| `internal/httpx/` | HTTP client, Yahoo API URL builders |
| `internal/metrics/` | Prometheus metrics |
| `internal/temporal/` | Temporal client configuration |
| `internal/matching/` | NHL ↔ Yahoo player matching |
| `internal/evenstrength/` | Per-game even-strength totals (`even_strength_pair_toi`, `even_strength_skater_games`) derived in Go from the stored shifts, box-score rows and goals, rebuilt by the shift chart import; `legacy_equivalence_pg_test.go` checks the output against the retired SQL derivation on real games (see https://wiki.spe.quebec/en/puckdb/season-sync-performance) |
| `internal/projection/` | Deterministic baseline projections for the draft helper: per-player season estimates (mean and interval) for every scoring statistic from regular-season NHL history available at an as-of time (skater and goalie models, aging curve, goalie share, team environment, even-strength linemate context), merged with the draftable pool and imported or manual overrides, stored as immutable snapshots whose config and source-data hashes make each one reproducible (`PGStore.BuildSnapshot`, `StoreSnapshotWith`); `Evaluate` scores a model version against a held-out season. Consumed by `draft` (`BuildRanking` scoring, stand-in pool history floor), `draftrank` (ranking refresh) and `newsadjust` (scenario snapshots) |
| `internal/draft/` | Draft helper models: normalized league rules, scoring-input validation, roster feasibility, player-pool coverage, comparison reports (see https://wiki.spe.quebec/en/puckdb/draft/league-rules) |
| `internal/draftrank/` | Draft ranking service shared by GraphQL, the CLI, exports and the live board: refresh (projection → news adjustment → rankings per scenario), immutable per-league snapshots, the status-bearing `Service.Read` (snapshot + dynamic freshness/override/rule/refresh issues + current rules hash) that `Rankings` and the board both use, the `Advice` policy per issue code (`AdviceOutdated` labels advice stale, `AdviceWithheld` = `ROSTER_SLOTS_CHANGED`/`SCORING_CHANGED` suppresses it), views (position/search/sort/pagination), explicit issue codes, CSV/JSON/table export (see wiki `puckdb/draft/rankings-api.md`) |
| `internal/draftrecommend/` | Deterministic live-draft recommendation and replay service over one ranking/session snapshot, with audited numeric reasons and PostgreSQL run storage (see https://wiki.spe.quebec/en/puckdb/draft/recommendations) |
| `internal/draftsession/` | Pure live-draft board reducer, with no persistence or Yahoo dependency (standard library only): `Reconcile` applies one Yahoo draft-results observation, `ApplyManual`/`ResolveConflict` the local add/correct/undo and explicit conflict choices, `EffectiveBoard`, the one-player-one-slot invariants and `SafeToRecommend`; callers persist the returned `State` in their own transaction (`draftwatch` for `draft_sessions`, `draftrecommend` for run inputs) (see wiki `puckdb/yahoo-draft-watch.md`) |
| `internal/draftwatch/` | Everything around the reducer that touches PostgreSQL or Yahoo, despite the name: league `Identity` resolution (`ResolveIdentity`: full key, or numeric ID with `NewestSeason` = the season whose rules were seen last) and `ParseTeamKey`/`ParsePlayerKey`; the `draft_sessions` `PGStore` (successful-poll reconciliation, manual add/correct/undo/resolve with an optional expected `state_version`, events, capability observations, failure records) and its board JSON codec; the `Runner` (its `SessionStore`; `SyncOnce`/`Watch` under the per-league PostgreSQL advisory lock, bounded backoff, one final reconciliation on cancellation); `YahooSource`, which force-refreshes league status and draft results through `worker/yahoo.Fetcher` (so this package, and `draftboard` and `mcpserver` through it, depend on `internal/worker` and the Temporal SDK). Used by `sync draft`, `draft session`, the API's live board (`draftboard`) and the MCP server's player keys (see wiki `puckdb/yahoo-draft-watch.md`) |
| `internal/draftboard/` | Live-board application service over a `DataSource` (the union of `IdentityResolver`, `SessionStore`, `ShortlistStore` and `LeagueSource`; `PGDataSource` in production): versioned Yahoo session + ranking/recommendation composition, roster feasibility, shortlist and process-scoped watch controls; rankings come from the API's `draftrank.Service.Read` (never a raw snapshot), its issues reach board warnings, `rankingIssues`, the recommendation input and the recommendation cache key (by code); persisted recommendations use a bounded process-local LRU with per-key request coalescing, and persistence runs outside the cache lock on a leader-independent bounded context (see https://wiki.spe.quebec/en/puckdb/draft/maurice-board) |
| `internal/draftboardui/` | Embedded responsive `/draft/` browser client with keyboard navigation and polling/version recovery; its league selector comes from `draftLeagues` (the server's current draft season, or `/draft/?season=<start year>`), each board using the season of its league row, and ships no league IDs (`TestLeagueSelectorShipsNoLeagues`); every response and versioned mutation is tied to one league/season selection generation. Its Node tests (`web/*_test.js`, real `app.js` in a VM with a fake DOM and deferred fetches) run through `TestClientScript` under `go test` (skipped without `node`, failing in CI); embedded assets are listed one by one so test files are not served |
| `internal/news/` | Player news for the draft helper: source set (`sources.yaml`), RSS/Atom, NHL content and Yahoo status adapters, conditional fetch, article versions, player resolution, incident grouping, coverage and reports (see https://wiki.spe.quebec/en/puckdb/draft/player-news) |
| `internal/newsadjust/` | News adjustments for the draft helper: validated event contract, versioned loading of stored extraction events (review and release-gate holds), as-of event selection (dedupe, supersession, returns, rumors), conservative/base/optimistic scenario snapshots, manager overrides, ranking comparison, run storage and replay (see https://wiki.spe.quebec/en/puckdb/draft/news-adjustments) |
| `internal/newsevent/` | LLM extraction of validated player news events: prompt and strict output schema, quote/claim/chronology validation, injection defenses, deterministic lifecycle reconciliation (active/superseded/retracted/resolved), labeled evaluation corpus (`evalcorpus.yaml`) and release gate (see https://wiki.spe.quebec/en/puckdb/draft/news-events) |
| `internal/yahooaccess/` | Yahoo API access check: game key and league settings probes, AUTHORIZED / NOT AUTHORIZED / ERROR classification, email subject and report (see https://wiki.spe.quebec/en/puckdb/yahoo-access-check) |
| `internal/yahookey/` | Canonical Yahoo league, team and player key value types, parsing and formatting; callers retain domain-specific ownership checks and error context |
| `internal/notify/` | Plain-text notification emails over SMTP submission (STARTTLS when offered) |
| `internal/appuser/` | PuckDB users and application sessions, per API instance (`--instance` / `PUCKDB_INSTANCE`, required by `api`: the main API and each Maurice instance share the database, and the same identity on two instances is two users with separate sessions and conversations): identity from the instance's one login provider (`--auth-provider` / `PUCKDB_AUTH_PROVIDER`): `authentik` (default; the trusted `X-authentik-uid`/`X-authentik-email` headers, the proxy must strip client copies) or `cloudflare` (the `Cf-Access-Jwt-Assertion` JWT, RS256-verified against the team's JWKS, cached and refetched on an unknown `kid` at most once a minute, with `iss` = `https://<--cloudflare-access-team-domain>`, `aud` ∋ `--cloudflare-access-aud`, `exp`; subject = `sub`, username and email = `email`; the unsigned `Cf-Access-Authenticated-User-Email` is never read); each provider strips the other's headers from every request (`IgnoreForeignHeaders`, before `httpx.HeaderContext`, so the `@admin` group and override authors cannot be forged) and a request carrying both is refused with 401; a random `puckdb_session` cookie stored only as an HMAC (`--session-hash-key`), lazy per-request resolution for the GraphQL route (`appuser.Current`); cookie-less callers join the user's active session (30 min idle timeout); the login upsert transaction also calls `yahoolink.SyncEmailLinks`. An optional members file (`--maurice-members`, `Members`/`WithMembers`, YAML: login email → optional `name` (first name) and `yahoo_email`; malformed, unknown field or case-insensitive duplicate fails `api` startup; read once, so an edit needs a restart) gives a login email's identity the entry's name as display name and greeting, and its Yahoo email as `app_users.member_yahoo_email`, matched besides the login email; `Identity.GreetingName` (`mauriceMe.firstName`) = the entry's name, else the first word of the provider's name, else the username (see wiki `puckdb/yahoo-account-links.md`) |
| `internal/yahoolink/` | Links a Maurice user to the Yahoo fantasy teams they manage, several per user, through a Yahoo account (guid) link (`app_user_yahoo_managers`: every team whose manager resolves to the guid, in every season, so a new season's teams need no new link) or, for a manager row resolving to no guid or a co-managed team linked by hand, a team link (`app_user_yahoo_teams`). Yahoo hides every current-season guid (`--hidden--`), so the `yahoo_team_manager_guids` view resolves such a row through the one real guid older rows with the same email carry (see wiki `puckdb/yahoo-account-links.md`). `SyncEmailLinks` matches `yahoo_team_managers.email` case-insensitively against the login email and the members file's `yahoo_email` for it on every authenticated request (called from `appuser.PGStore.Resolve`'s transaction) as `source=email`, never touching a `source=manual` link added by `puckdb user link-team`/`unlink-team`; `PGStore.ListUserTeams` (each team once, with its league's name and season, Yahoo's start year) serves Maurice's system prompt, limited to an optional league ID allowlist (`ParseLeagueIDs`, `--maurice-yahoo-leagues`); `DefaultSeason` (the newest linked season not after the current NHL season, so the off-season keeps one) picks the teams the prompt calls "my team", the other seasons' teams follow on one line capped at `maxOtherSeasonTeams` (`internal/maurice/prompt.go`), only for questions about that season. `Picker` (GraphQL `mauriceTeamChoices`/`mauriceSetTeams`/`mauriceDismissTeamOffer`) lets a user pick their own teams among the teams of `DefaultSeason` over the allowlisted leagues' seasons: a pick is a `source=self` link of the kind `link-team` makes (an email link to the team becomes the pick; self-declared, unverified, only that user's prompt; co-managers may pick the same team), and a linked choice left out is deselected (`app_user_yahoo_team_rejections`: hidden whatever link reaches it, no email team link to it; cleared by picking it again, by `link-team`, or by `unlink-team` once neither the login email nor the member Yahoo email matches one of the team's managers or resolves to a guid one of them resolves to) |
| `internal/fixtures/yahoofixtures/` | Synthetic Yahoo XML fixtures shared by tests (test-only import) |
| `internal/fixtures/metricsfixtures/` | Reads filesystem operation counts from the worker metrics registry for label assertions (test-only import) |
| `internal/fixtures/draftfixtures/` | Synthetic draft ranking snapshot and in-memory store shared by the draftrank, GraphQL and CLI tests (test-only import) |
| `internal/fixtures/projectionfixtures/` | Default projection config with the aging curve the aging model versions must store, for tests that hand-build projection snapshots (test-only import) |
| `internal/fixtures/pgtest/` | The shared PostgreSQL test harness: `PUCKDB_TEST_PG_URL` skip, `CheckURL` (a `postgres://`/`postgresql://` URL whose parsed database name contains `test`, case-sensitively, on one server (no host list), with the same host, port, user and database for pgx and lib/pq, the migrator's driver; the marker elsewhere in the URL does not count), migrate once per test binary, `Open(t, truncate...)`, `Truncate`/`TruncateRestartIdentity`; every database-backed test reaches the database through it, except `internal/database` (import cycle; private schemas/databases only) and the simulation `integration`/`livellm` `TestMain`, which only calls `CheckURL`, then migrates the database down and up and opens its own pool (see wiki `puckdb/postgres-test-harness.md`) (test-only import) |
| `internal/fixtures/mauricefixtures/` | Constraint-satisfying inserts of Maurice users, conversations, turns, messages, LLM calls (with retained inputs and call-message links) and tool calls (any status, transcript-only, malformed arguments) with explicit timestamps, shared by the `mauricereport` and GraphQL reporting tests (test-only import) |
| `internal/fixtures/simfixtures/` | A private, migrated PostgreSQL database per test (created next to `PUCKDB_TEST_PG_URL`, dropped at cleanup) and typed inserts for sim pools, agents, turns, tool calls, players and draft picks, shared by the GraphQL sim feed and `sim tail`/`sim draft` tests (test-only import) |
| `internal/llm/` | LLM client and the shared `agentloop` tool loop (used by player enrichment, Maurice and the simulation) |
| `internal/maurice/` | Prompt + service layer built on top of `internal/llm/` |
| `internal/mauricereport/` | Read-only cross-instance Maurice reporting behind the GraphQL `@mauriceReporting` fields: instances, an instance's users and its conversations' metadata (lists and aggregates never carry messages or payloads), every lookup scoped through `app_users.instance` (another instance's user = `ErrUserNotInInstance`, its conversation = not found), deleted conversations only with `IncludeDeleted`; turn filters (UTC-day interval on `started_at`, stored status, provider/model, tool name) hold for one same turn, search is an escaped `ILIKE` over titles and user/assistant message text; keyset pages on immutable keys with opaque cursors bound to their list, instance and filter, `DefaultLimit` 50, `MaxLimit` 200; `Overview` and `UserActivity` aggregate what the same filter includes (the listed conversations, their turns meeting the turn filters, those turns' LLM calls and the in-interval title calls; status/tool filters exclude title calls) in one repeatable-read snapshot: turn counts per status with rates over terminal turns, the four token categories each with reported/unreported call counts (unknown is never zero), provider/model breakdown, succeeded turn/call latency, processing time as the per-conversation union of turn and title-call intervals; never `app_user_usage`; one conversation's detail (`detail.go`, `pgdetail*.go`, `pgpayload.go`): transcript (user/assistant text inline, tool results and system messages only on request; messages of non-succeeded turns marked not replayable), turns with usage, a turn's calls, title calls apart, tool calls, a call's exact retained input and a tool call's arguments/result, each read in one snapshot after re-checking the conversation's instance and deletion and that every nested turn/call/tool/message ID belongs to it; texts inline at most `InlineTextRunes` code points with truncation marked, the rest through `Payload` windows (`MaxPayloadRunes`) (see wiki `puckdb/maurice-reporting-api.md`) |
| `internal/mcp/` | MCP client (used by Maurice to call tool servers) |
| `internal/mcpserver/` | MCP server exposing curated read-only data tools (`mcp-server` command), split into the `nhl`, `yahoo` and `news` toolsets; the `news` toolset (`get_news_coverage`, `get_player_news`, `get_recent_news_events`; `get_player_news` reads through `newsevent.LoadPlayerEvents`, no new SQL) is not scoped to a Yahoo league, and bounds the quoted article text (reports per event, quotes per report, runes per quote); the `yahoo` toolset's draft ranking and override tools (`get_draft_leagues`, `get_draft_rankings`, `get_draft_player_detail`, the read-only `get_draft_overrides`; creating and resetting overrides stay in GraphQL) read through the `draftrank.Service` the command passes in `Options.Draft` (required with `yahoo`), never new SQL, while its draft report tools (`get_draft_pool_coverage`, `get_draft_rules_report`) build `draft.LoadLeagueReport` reports from `draft.Queries`; `get_draft_rankings` returns a league's whole ranked pool (up to `maxDraftRankingsLimit` rows) as one header plus CSV sections, numbers rounded (`compactFloat`), a players table with one projected-total column per category (labels from `draftrank.CategoryLabels`, shared with the CSV export), and refuses output above `maxDraftRankingsOutputBytes`; `league_guard.go` refuses Yahoo leagues outside `--mcp-yahoo-leagues` (refusal = unknown league), and every league-scoped Yahoo tool must go through `leagueGuard.scoped` (numeric `league_id`) or, for the draft tools taking `season` plus `league` (key or ID), `leagueGuard.scopedKey`/`resolveKey` (enforced by `TestYahooToolsAreLeagueGuarded`); integer arguments go only through the strict helpers in `tools_common.go` (`requireInt`/`optionalInt`/`optionalInt4Filter`/..., bounds per argument, checked before any narrowing cast; never the SDK's truncating `GetInt`/`RequireInt`, enforced by `TestToolsReadIntegersOnlyThroughStrictHelpers`), and `TestNHLToolsRejectMalformedIntegersBeforeSQL` / `TestNewsToolsRejectMalformedIntegersBeforeSQL` sweep every NHL / news tool's number arguments |
| `tls/` | TLS certificates for internal services (gitignored) |

Design notes and runbooks live on the shared wiki, not in this repository (see the `wiki` skill); this `CLAUDE.md` links the current page for each topic. Non-Go directories: `examples/` (sample pool-simulation and gob-cache configs), `scripts/` (operational shell scripts), `.docker/` (compose-only config for Temporal and Grafana provisioning).

License: GPL-3.0-or-later (`LICENSE`, copyright notice in `COPYRIGHT`). A new dependency must be GPLv3-compatible (MIT, BSD, ISC, Apache-2.0, MPL-2.0 are; GPL-2.0-only, SSPL, BUSL are not). The image ships every linked module's license/NOTICE files under `/licenses` via `scripts/collect-licenses.sh`, which fails on a module without a license file.

### Active Workflows

Defined in `internal/worker/workflow/`:
- `FetchSeasonsWorkflow` / `FetchNHLSeasonWorkflow` / `FetchYahooSeasonWorkflow` — NHL season data (parent + children); the parent runs the Yahoo and NHL season groups concurrently (the import runs them in order: day-level Yahoo roster rows need the imported teams), then fetches the upcoming (not yet started) season's camp rosters for the prior season's clubs. `FetchNHLSeasonWorkflow` and `ImportNHLSeasonWorkflow` are thin wrappers over one runner, `runSeasonDays` (`workflow/nhl_season.go`), with a `seasonDaysMode` holding each difference once, including where the day group's `StartGroup` save sits (recorded in history; `testdata/nhl_season_*.json` and its negative control pin it); fetch counts one origin per unit of progress (daily schedules, club schedules), import the daily schedule, game and Yahoo file reads of each day and each playoff game's boxscore, not NHL standings, club schedules, season rosters or club stats (see wiki `puckdb/plans/nhl-season-runner.md`). `FetchYahooSeasonWorkflow` also captures each league's current standings (`resource.LeagueStandings`, `league/<key>/standings`, no date filter at Yahoo) under the capture day in `America/Los_Angeles` (`resource.StandingsCaptureDate`): the first capture of a day is the day's snapshot, nothing is ever backfilled for another day, and days outside the league's season are skipped (see https://wiki.spe.quebec/en/puckdb/plans/yahoo-team-standings)
- `ImportSeasonsWorkflow` / `ImportNHLSeasonWorkflow` / `ImportYahooSeasonWorkflow` — Parse cached files into Postgres; the parent also carries the prior season's clubs forward to the upcoming season (`season_teams`) and imports its camp rosters. `ImportNHLSeasonWorkflow` runs `ImportDayIfChanged` per day: a day whose cached files (path, size, mtime; the Yahoo files are `yahoo.DateInputs`: rosters, summaries and each league's standings snapshot) and `worknhl.DayImporterVersion` match its `nhl_day_imports` row is skipped (a day whose game-story/season-series rows or Yahoo files were skipped after errors is not recorded, so it is retried); bump `DayImporterVersion` whenever the same files would produce different rows, and use `sync --reimport-days` (GraphQL `SeasonsInput.reimportDays`) to force a full re-import
- `FetchPlayerLogsWorkflow` / `FetchSeasonPlayerLogsWorkflow` — Per-player game logs
- `ImportPlayerLogsWorkflow` / `ImportSeasonPlayerLogsV2Workflow` — Import those logs: the parent lists each season's unique skaters once from the cached boxscores (`CollectSeasonSkaters`, reporting unreadable or missing schedules and boxscores in the scan group's message), sizes the season's bar from that list and passes it to the child, so both bars count the same players. `GetVersion("import-player-logs-skater-manifest")` keeps runs started before it on the legacy Redis-count path and `ImportSeasonPlayerLogsWorkflow` child (`import_player_logs_legacy.go`), to be removed once none runs
- `FetchPlayerLandingsWorkflow` — NHL player landing pages
- `ExtractBoxscorePlayersWorkflow` — Extract player rows from boxscores
- `FetchYahooPlayersWorkflow` — Downloads Yahoo's NHL games list, then the due game player lists for up to `--yahoo-game-player-list-concurrency` seasons at once (default 4). Finished seasons are final unless Yahoo changes their game key (a list downloaded for another game key is fetched again); the current season refreshes after `--yahoo-game-player-list-max-age` hours. Each season fetches pages sequentially until a short page or an offset Yahoo rejects (400) as past the list's end, then commits a manifest; reaching `yahoo.MaxGamePlayerListPages` fails the season; failed downloads discard uncommitted pages. `FetchYahooPlayerBatch` remains registered only for recorded Temporal histories (see wiki `puckdb/yahoo-game-player-lists.md`).
- `ProcessPlayersWorkflow` — Builds the Yahoo ID pool from committed game player lists and named `yahoo_league_players` in `BuildYahooIDPool`; the newest list entry wins for each Yahoo ID. A chain with a non-empty boxscore list stores a manifest and carries its cursor, totals, pool size and list seasons across ContinueAsNew; an empty list ends the run without one. `EnsureYahooIDPool` refreshes Redis TTLs, restores a missing available set, or rebuilds a lost pool from the lists; a rebuild fails if a season the chain read has no committed list or a named page cannot be read. Batches download and cache `resource.YahooPlayer` pages only when `matching.BirthDatesNeeded` finds a jersey-tied name match needing a birth date; a page answering 404 (a retired player's) is recorded as `resource.MissingYahooPlayer` (`yahoo-players-missing/`) and not asked for again for `missingYahooPlayerPageTTL` (30 days), counted as `BirthDatePagesMissing`. Name matches still tied after the birth date are told apart by `matching.MatchYahooIDBySeasons` (reason `name+seasons`): the seasons whose game player lists name each candidate (`store.YahooPlayer.Seasons`) against the player's NHL seasons, picking a candidate only when every candidate has list seasons, it alone overlaps and no namesake's NHL seasons (`ListNamesakeNHLSeasons`) overlap it. `ListYahooPlayerFiles`, `ParseYahooPlayerBatch`, and `SaveYahooPlayersToRedis` remain registered only for recorded Temporal histories. The separate pre-manifest workflow branch remains versioned by `process-players-manifest` (see wiki `puckdb/yahoo-game-player-lists.md`).
- `FetchEdgeSeasonsWorkflow` / `FetchEdgeWorkflow` — NHL Edge tracking data (2021-2022+)
- `ImportEdgeSeasonsWorkflow` / `ImportEdgeWorkflow` — Import cached Edge data into Postgres
- `RefreshNewsWorkflow` — Fetch due player news sources, store new article versions, resolve players into incident candidates, extract validated events with an LLM when the worker runs with `--news-extract-enabled` (capped calls/tokens per run), prune (on demand, or via the `refresh-news-schedule` Temporal schedule when the worker runs with `--news-schedule-minutes`)
- `RefreshDraftRankingsWorkflow` — Recompute each league's draft ranking snapshot (baseline + news scenarios) one league at a time; a failed league keeps its last snapshot; rejected while running (fixed workflow ID)
- `InitializeWorkflow` — Database initialization
- `ScheduledSeasonsSyncWorkflow` / `ScheduledPlayersSyncWorkflow` — the current season's `sync seasons` (fetch then import; the import also runs after a failed fetch) and `sync players` (`yahoo-players` through `import-player-logs`, stopping at the first failure), started by the worker's Temporal schedules `seasons-sync-schedule` / `players-sync-schedule` (`--seasons-sync-schedule`, `--players-sync-schedule`: cron expressions in `--sync-schedule-time-zone`, default `America/Toronto`; empty, the default, removes the schedule; overlap skipped, 12 h catch-up). The season is picked from `workflow.Now` at each run (July rollover). Each step runs as a child under the fixed workflow ID a manual sync uses (`fetch-seasons`, `import-seasons`, ...), so the GraphQL progress queries show it and a manual sync of a running step is refused; a step found already running ends the scheduled run as skipped (`ScheduledSyncResult.SkippedRunning`), leaving the later steps to that sync

Defined in `internal/worker/admin/`:
- `DropDatabaseWorkflow`, `MigrateDatabaseWorkflow`, `ResetDatabaseWorkflow`, `FlushRedisWorkflow`

Defined in `internal/worker/simulation/`:
- `SimPoolWorkflow` — one simulation pool, workflow ID `sim-pool-<pool id>`, started by `createSimPool`: team names, snake draft, then one iteration per calendar day of the window (`ProcessWaivers`, `BuildFreeAgentPool`, per agent `BuildManageRosterContext` + `ManageRoster` in a shuffled order, `CollectDayStats`, `UpdateStandings`), Continue-As-New every 30 days, then `status = 'complete'`. `PoolConfig.StopAfter` (`never`/`team_name`/`draft`/`season`) ends it early as complete and `MaxSeasonDays` caps the day loop; the only pause is the cost cap (the LLM activity marks the pool `paused` and returns `SkipReasonCostCapReached`; nothing resumes it); `cancelSimPool` cancels it (`cancelled`). No signals; the only query is the shared progress report (see wiki `puckdb/simulation.md`)

Task queue: `puckdb-tasks`

Every activity the worker registers must be scheduled by workflow code or listed in `historyOnlyActivities` (`cmd/worker_registrations_test.go`) with the still-registered workflow whose histories recorded its name (`TestRegisteredActivitiesAreScheduledOrRecorded`); a method that only runs inside another activity (`ImportDay`'s per-file imports, `FetchDailySchedule`, `ExtractBoxscoreDataForSeason`) is not registered. Never unregister a name only because current code no longer schedules it: check the history first (see wiki `puckdb/worker-activity-registrations.md`)

Activity failures are classified at the worker boundary: `shared.FailureClassifierInterceptor` (installed on every queue, asset queue included, by `workerOptions()` in `cmd/worker.go`) turns a cached file that does not parse (`resource.ParseError`, type `CorruptCachedFile`), a value PostgreSQL rejects (SQLSTATE 22001/22003/22007/22008/22021/22P02/22P05/23502/23514, type `RejectedValue`) and a Yahoo call with no usable OAuth2 token (`cache.OAuth2TokenMissingError`: none stored, or Yahoo rejected the refresh token; type `YahooLoginRequired`, fixed only by a new `/yahoo/login`) into non-retryable errors; cancellations, explicit Temporal errors, missing cached files (a concurrent fetch may still write them), foreign key/unique violations and transient database errors keep the retry policy. Keep wrapping causes with `%w` so the classifier sees them (see wiki `puckdb/activity-failure-classification.md`)

### Progress Tracking Pattern for Child Workflows

When a parent workflow spawns child workflows and displays per-child progress bars, each **child workflow must have its own tracker** so the parent can query it for progress. Without this, progress bars stay at 0.

**Pattern:**

```go
// 1. Create a progress report with Total matching the bar size
func NewFetchEdgeProgressReport(season nhl.SeasonInfo) *shared.ProgressReport {
    total := 192 // must match what parent's Counter function returns
    return &shared.ProgressReport{
        Total: total,
        Groups: []shared.ProgressGroup{
            {Header: fmt.Sprintf("Fetching Edge %s...", season.Label()),
             Bars: []shared.ProgressBar{{Total: total}}},
        },
    }
}

func FetchEdgeWorkflow(ctx workflow.Context, input FetchEdgeWorkflowInput) (core.OriginCounts, error) {
    // 2. Create tracker and register query handler (makes progress queryable)
    tracker, err := shared.InitTracker(ctx, NewFetchEdgeProgressReport(input.Season))
    if err != nil {
        return nil, err
    }
    tracker.StartGroup(ctx, 0)

    // 3. Run activities, incrementing progress as each completes
    for _, f := range futures {
        if err := f.Get(ctx, nil); err != nil {
            return counts, err
        }
        tracker.IncrementBar(ctx, shared.BarRef{Group: 0, Bar: 0})
    }

    // 4. Mark complete when done
    tracker.CompleteGroup(ctx, 0, fmt.Sprintf("Done in %s.", tracker.GetElapsed(ctx, 0)))
    return counts, nil
}
```

**Key points:**
- Parent uses `ChildIDFunc` to set `bar.ChildWorkflowID = "fetch-edge-2024"`
- Parent's progress display queries child workflows by their workflow IDs
- If child has no tracker/query handler registered, query returns empty → bar stays at 0
- Child's progress report Total must match parent's `Counter` function return value
- Return `core.OriginCounts` for aggregation in parent (separate from progress tracking)
- Bar methods (`IncrementBar`, `IncrementBarBy`, `StartBar`, `CompleteBar`, `ResizeBar`, `SetBarTotal`, `Bar`) take a `shared.BarRef`; a workflow that names the same bar several times declares it once (`var upcomingSeasonBar = shared.BarRef{Group: groupUpcomingSeason}` in `upcoming_season.go`). A `BarRef` outside the report panics with a message naming the reference, the group count and, when the group exists, its bar count; a bare group index outside the report (group methods such as `StartGroup`, `CompleteGroup`) panics naming the index and the group count

**Reference implementations:**
- `FetchNHLSeasonWorkflow` — uses `RunWorkerPool` for automatic tracking: `tracker.RunWorkerPool(ctx, shared.PoolOptions{Total: n, Concurrency: c, Start: start, Handle: handle, Progress: shared.SingleBar{Bar: shared.BarRef{Group: g}}})` advances one bar per completed item (by `SingleBar.Increment(i)` when set); `shared.BarPerItem{Group: g}` gives item i its own bar instead, `shared.BarsPerItem{Group: g, Ranges: r}` the bars `r[i]` (StartBars on dispatch, CompleteBars after the handler, which may resize them). Every mode advances a failed item too unless `OnlyOnSuccess: true`, which advances progress only after `Handle` (or `future.Get`) succeeded. `RunWorkerPool` validates the options (bar references included; a pointer progress mode is rejected) and returns an error before issuing any command, except that a zero `Total` returns nil unvalidated
- `PoolOptions.SaveEvery` optionally batches progress persistence while every item still updates the in-memory report used by the `progressReport` Temporal query. Values 0 and 1 save on every hook; a larger value saves after each batch, the final completion and an error, so the Redis-backed GraphQL progress may lag by at most `SaveEvery-1` completions. The NHL fetch and import season day loops use `config.DefaultDayLoopProgressSaveEvery` (10), gated by `dayLoopBatchedProgressChangeID` so earlier histories retain per-hook saves; other pools keep the default
- Batched pools — `shared.NewBatches(items, size)` (a slice) or `shared.NewBatchRanges(n, size)` (an ID range) gives the pool's `Total` (`Count()`), each item's data (`Batch(i)` / `Bounds(i)`) and, for a bar sized in items, `ItemProgress(bar)` (advances by each batch's real length, so a short last batch never overcounts); never recompute counts or slices by hand
- Tracker-less pools — the package-level `shared.RunWorkerPool(ctx, shared.PoolOptions{...})` runs the same scheduler with no progress and rejects a non-nil `Progress`; `PoolOptions.StopDispatch` (both entry points) ends dispatching once it returns true while in-flight items still reach `Handle` (`yahoo_pool_pages.go`)
- `FetchEdgeWorkflow` — uses manual `IncrementBar` calls

### Configuration Inside Workflow Code

Workflow code must never read viper flags or config files directly. Any setting that shapes the command sequence (concurrency, batch sizes, which Yahoo leagues to process) is resolved **once at the start of the workflow** through `shared.SnapshotConfig` / `shared.SnapshotConfigInt`, which records the value in history via `SideEffect` so a replay after a worker restart reuses it instead of re-reading changed local settings. ContinueAsNew runs carry the snapshot in their input (see `processPlayersState.Config` in `process_players_manifest.go`). Activity options (timeouts, retry policy) are not part of the determinism check and may stay live. Reference: `internal/worker/workflow/season_config.go`.

## External Services

| Service | Port | Details |
|---------|------|---------|
| PostgreSQL | 5432 | user: `puckdb`, password: `foo`, db: `puckdb` |
| Redis | 6379 | password: `redis` |
| Temporal | 7233 | namespace: `default` |
| Temporal UI | 8080 | Workflow monitoring |

## Data Flow

1. User authenticates via Yahoo OAuth2 (`/yahoo/login` → Redis)
2. GraphQL mutations or CLI trigger Temporal workflows
3. Workers download from NHL/Yahoo APIs → cached files
4. Workers parse cached files → PostgreSQL
5. GraphQL queries serve from PostgreSQL

## Configuration Conventions

Flags follow a strict pattern in `internal/config/`:

1. **Flag names** → constants in `flags.go` (e.g., `FlagAPIPort = "api-port"`)
2. **Defaults** → constants in `defaults.go` (e.g., `DefaultAPIPort = 8787`)
3. **No magic numbers** in flag definitions
4. **Shared flags** → `FlagDef` entries in named `FlagGroup` values in `internal/config/flags.go`; logging flags are the exception, registered and bound with `config.InitLoggingFlags`/`config.BindLoggingFlags`, and command-specific flags read directly by that command may stay local (`metrics --port` is the intentional alias exception)
5. A command declares its `[]*config.FlagGroup` once and passes the same list to `config.InitFlags` and its binding path: normally `bindFlagsPreRunE`, or `config.BindFlags` in a custom pre-run hook when extra setup is required (`syncInit` and `metrics`). `draft session` registers shared groups once on the parent's persistent flags; each leaf registers its local groups and binds the shared and local groups together. Direct group `Init`/`Bind` methods are reserved for root persistent wiring (`root.go` and `syncInit`)
6. **Library packages take values, not viper.** `store`, `cache`, `database`, `temporal`, `httpx` and `graph` get their settings as options (`database.Config`, `temporal.Options`, `cache.Options`, `httpx.YahooAuth`, `graph.AdminAuth`, `Resolver.PublicURL`/`YahooSeasons`, a data path). The `xxxFrom(v *viper.Viper)` readers in `cmd/settings.go` build them; `cmd/settings_test.go` checks the flag defaults still produce each package's `Default*Options()`. `TestLibraryPackagesDoNotReadViper` (`internal/config/viper_boundary_test.go`) fails on a viper import or a call to a viper-reading `config` helper in those packages

Season config: `seasons.yaml` (start/end dates, game keys, league IDs, team IDs)

## Logging

Everything logs through the global zerolog logger (`config.SetupLogger`); the Temporal client wraps the same logger. Full convention on wiki `puckdb/logging.md`, enforced by `internal/config/log_convention*_test.go`:
- Workflow code logs only through `workflow.GetLogger(ctx)` (silent during replay); a worker file with `workflow.Context` code must not import `github.com/rs/zerolog/log`
- An activity method logs through `activity.GetLogger(ctx)` (adds the workflow and activity IDs), never mixed with the global logger in one function (`TestActivityMethodsDoNotMixLoggers`); helpers that also run outside an activity keep zerolog (`activity.GetLogger` panics on a non-activity context)
- Keys are snake_case string literals (`player_id`, `start_year`), the error key is `error`, Temporal calls pass paired keys and values; so do the arguments of a function forwarding `keyvals ...any` to a Temporal logger (`logFetched`)
- Library packages never write to the process's stdio (`fmt.Print*`, `os.Stdout`, ...), apart from the logger setup in `internal/config/log.go`: user output goes to an `io.Writer` from the caller (the Maurice REPL's `REPLConfig.In`/`Out`/`Err`); CLI output in `cmd/` is not logging and stays on `fmt`
- HTTP and GraphQL panics reach zerolog: `middleware.Recoverer` after `httpx.ChiLogger`, `newGraphQLRecover` on the GraphQL server, `config.ServerErrorLog()` as the API and metrics servers' `ErrorLog`

## GraphQL API

Schema lives in `internal/graph/schema.graphqls`. Each long-running workflow follows the same pattern: a `start` mutation, a matching `cancel*` mutation, a `*Result` query, and a `*Progress` query.

**Workflow mutations** (each has a paired `cancel<Name>`):
- `initialize` — Database initialization
- `fetchSeasons(input: SeasonsInput)` — Download NHL season data
- `importSeasons(input: SeasonsInput)` — Parse cached NHL files into Postgres (unchanged days are skipped; `reimportDays: true` imports every day)
- `fetchPlayerLogs(input: SeasonsInput)` / `importPlayerLogs(input: SeasonsInput)`
- `fetchPlayerLandings(input: FetchPlayerLandingsInput)`
- `extractBoxscorePlayers(input: SeasonsInput)`
- `fetchYahooPlayers`
- `processPlayers(input: ProcessPlayersInput)` — Player enrichment + matching
- `fetchEdgeStats(input: SeasonsInput)` / `importEdgeStats(input: SeasonsInput)` — Edge tracking data
- `refreshNews(input: RefreshNewsInput)` — Player news refresh (`force`, `sources`, `season`)
- `refreshDraftRankings(input: RefreshDraftRankingsInput)` — Draft ranking snapshots (`season`, `leagueIds`, bench/workload/uncertainty options)

**Admin mutations:** `clearDatabase`, `dropDatabase`, `createDatabase`, `flushRedisDB`, `resetTeamOffer`, `resetInstanceTeamOffers` (team picker offer, below)

**Maurice (LLM chat) mutations:** `mauriceChat(conversationId, message, idempotencyKey)` (blocks until the answer), `mauriceChatStart(conversationId, message, idempotencyKey!)` (returns `{turnId, conversationId}` at once; poll `mauriceTurn(id)`: `RUNNING` (its `toolsUsed` lists each tool as its call starts, read from `maurice_turns.tool_names`, so any replica answers)/`SUCCEEDED` with the answer/`FAILED` with the error class and the tools whose start was recorded, a shutdown-cancelled turn included; null for another user's turn), `mauriceDeleteConversation(id)`. A turn runs detached from its request (`internal/maurice/turnrun.go`), bounded by `turnTimeout`; a client that disconnects does not lose it, and `Service.Close` (server shutdown) cancels it, committed as cancelled. Every Maurice field needs an identity of the instance's login provider (`X-authentik-uid`, or a valid Cloudflare Access JWT) and is scoped to that user's conversations; resending an `idempotencyKey` with the same prompt returns the original answer (`mauriceChatStart`: the same turn, even while it runs; `mauriceChat` rejects the key of a running turn); on both mutations a different prompt under the same key, or a second prompt while a turn runs, is an error, and so is a new turn beyond the per-user or per-process cap on running turns

**Maurice team picker** (in `internal/graph/maurice_teams.graphqls`, `yahoolink.Picker`; same identity requirement and per-user scoping): `mauriceTeamChoices` (the default season's teams of the `--maurice-yahoo-leagues` leagues with league/team names and manager nicknames, never emails or guids, whether the caller is linked and by which source, and `offered`), `mauriceSetTeams(teams: [TeamRef!]!)` (replaces the caller's selection within those choices, refusing any team outside them, and marks the offer seen), `mauriceDismissTeamOffer`. Not scoped to the caller and needing no login identity, the `@admin` mutations `resetTeamOffer(instance, username)` (whether the user had seen the offer; `extensions.code` `USER_NOT_FOUND`/`AMBIGUOUS_USER`) and `resetInstanceTeamOffers(instance)` (how many had) clear `team_offer_seen_at` for `puckdb user reset-team-offer`

**Workflow queries:** for every workflow above, `<name>Result: WorkflowResult!` and `<name>Progress: ProgressReport`. `processPlayers` additionally exposes `processPlayersResultData: ProcessPlayersResultData`.

**Data queries** (in `internal/graph/data.graphqls`): `latestNHLDataDate`, `seasons`, `teams`, `players`, `games`, `standings`, `boxscore`, `skaterGameLog`, `goalieGameLog`, `playerSeasonTotals`, `edgeSkaterStats`, `edgeGoalieStats`, `edgeTeamStats`

**Draft rankings** (in `internal/graph/draft.graphqls`, see wiki `puckdb/draft/rankings-api.md`): `draftLeagues`, `draftRankings`, `draftPlayerComparison`, `draftOverrides`; mutations `createDraftOverride`, `resetDraftOverride`.

**Simulation** (in `internal/graph/simulation.graphqls`, see wiki `puckdb/simulation.md`): `simPool`, `simPools`, `simTransactions`, `simStandingsHistory`, `simPoolProgress`, `simTurnFeed`, `simDraftPicks`; mutations `createSimPool`, `cancelSimPool`. Every field reads PostgreSQL (progress: the Redis report), never the running workflow.

**Maurice reporting** (in `internal/graph/maurice_reporting.graphqls`, `maurice_reporting_overview.graphqls` and `maurice_reporting_detail.graphqls`, see wiki `puckdb/maurice-reporting-api.md`): `mauriceReportingInstances`, `mauriceReportingUsers(instance)`, `mauriceReportingConversations(filter)`, `mauriceReportingConversation(instance, id, includeDeleted)`, `mauriceReportingOverview(filter)`, `mauriceReportingUserActivity(filter)`, read-only metadata and aggregates; conversation detail in `maurice_reporting_detail.graphqls`: `mauriceReportingTranscript`, `mauriceReportingTurns`, `mauriceReportingTurnCalls`, `mauriceReportingTitleCalls`, `mauriceReportingToolCalls`, `mauriceReportingCallInput`, `mauriceReportingToolCall`, `mauriceReportingPayload`, each taking a `MauriceReportingConversationRef` (instance, id, includeDeleted) and null outside it. Each field carries `@admin` and `@mauriceReporting` (`TestReportingFieldsCarryBothDirectives`); the latter requires `--maurice-reporting-enabled` (default off), the `authentik` login provider, an `X-authentik-uid` and `--admin-group` in `X-authentik-groups`, and never accepts `X-Admin-Token`; the api refuses to start with it enabled on a Cloudflare instance or without an admin group.

**Other queries:** `buildNumber`, `yahooTokenStatus`, `mauriceConversations(limit)`, `mauriceConversation(id)`, `mauriceTurn(id)`, `mauriceModel` (`{provider, model}`: the model `cmd/api.go` configures the Maurice service with, not what a provider reports per call; any user of the instance, `internal/graph/maurice_model.graphqls`; maurice-web relies on its name and shape), `mauriceMe` (`firstName`, the caller's greeting name, `internal/graph/maurice_me.graphqls`; needs a login identity)

## Metrics

The Prometheus registry is split in three (see `internal/metrics/metrics.go`):
- **Worker** registry — exposed by `worker` on `/metrics` (port 8788)
- **API** registry — exposed by `api` middleware
- **Collector** registry — exposed by the standalone `metrics` command, which scrapes cache/Redis/DB state

### Worker metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_fs_operation_duration_seconds` | Histogram | `operation`, `file_type` | Filesystem operation duration; `file_type` is the resource's `core.FileType`, passed by the `resource.Read`/`Write`/`Exists`/`Delete`/`Stat` helpers or `store.WithFileType`, and `Unknown` (first one per operation logged as a warning) when a caller passes none |
| `puckdb_fs_bytes` | Histogram | `operation`, `file_type` | Read/write sizes |
| `puckdb_http_request_duration_seconds` | Histogram | `api`, `method`, `status_code` | External API request duration |
| `puckdb_http_response_bytes` | Histogram | `api` | Response body sizes |
| `puckdb_download_total` | Counter | `file_type`, `result` | Downloads by result (hit/miss/error) |
| `puckdb_activity_duration_seconds` | Histogram | `activity` | Temporal activity duration |
| `puckdb_db_query_duration_seconds` | Histogram | `query` | sqlc query duration, including row consumption; raw untagged SQL is excluded |
| `puckdb_db_pool_connections` | Gauge | `state` | pgx pool connections (`acquired`, `constructing`, `idle`, `max`, `total`) |
| `puckdb_db_pool_*` | Counter | optional `reason` | pgx acquire/wait/create/destroy totals and durations from `pgxpool.Stat()` |
| `puckdb_valkey_cache_requests_total` | Counter | `cache`, `result` | Valkey-backed GobCache reads by bounded cache name (file type or `gob`) and hit/miss/error result |
| `puckdb_sim_llm_call_duration_seconds` | Histogram | `provider`, `model`, `agent_name` | Simulation LLM call latency (the `instrumentedLLMClient` around every agent) |
| `puckdb_sim_llm_failures_total` | Counter | `provider`, `model`, `reason` | Simulation LLM call failures |
| `puckdb_sim_day_duration_seconds` | Histogram | `pool_id` | Wall time of one simulated day (measured with `workflow.Now`, observed by `RecordDayDuration`) |

The worker's `/metrics` also serves the Temporal Go SDK's own `temporal_*`
series (`internal/temporal/metrics.go`, via `go.temporal.io/sdk/contrib/tally`),
registered on the same `WorkerRegistry`: `temporal_activity_schedule_to_start_latency_seconds`
and `temporal_workflow_task_schedule_to_start_latency_seconds` (time a task sits
ready before a poller picks it up — the queueing signal this worker previously
had no way to report), `temporal_worker_task_slots_available` /
`temporal_worker_task_slots_used`, and `temporal_poller_start_total`. These are
distinct from the `temporal_*` series already in Prometheus from the Temporal
server's own internal workers. tally's Prometheus sanitizer replaces any
non-alphanumeric character in a label value with `_`, so `task_queue` reads
`puckdb_tasks`, not `puckdb-tasks`, on these series (unlike the server's own
`temporal_*` series, which keep the dash) — match on the sanitized form.

### API metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_http_request_duration_seconds` | Histogram | `api`, `method`, `status_code` | API server request duration (separate registry from the worker's HTTP client metric of the same name) |

### Collector metrics (cache / Redis / DB)
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_cache_files_expected` | Gauge | `season`, `file_type` | Expected file count per season/type |
| `puckdb_cache_files_found` | Gauge | `season`, `file_type` | Actual file count per season/type |
| `puckdb_cache_completeness_percent` | Gauge | `season`, `file_type` | found/expected × 100 |
| `puckdb_cache_stats_last_updated_timestamp` | Gauge | — | Unix ts of last cache scan |
| `puckdb_cache_stats_compute_duration_seconds` | Gauge | — | Time taken by last cache scan |
| `puckdb_cache_disk_size_bytes` | Gauge | — | Bytes used by cache directory |
| `puckdb_data_path_files_total` | Gauge | `file_type` | Total files in data path by type |
| `puckdb_data_path_bytes_total` | Gauge | `file_type` | Total bytes in data path by type |
| `puckdb_redis_oauth_token_valid` | Gauge | `user` | 1 = usable token (fresh access token or a refresh token), 0 = missing/unrefreshable |
| `puckdb_redis_last_updated_timestamp` | Gauge | — | Unix ts of last Redis scan |
| `puckdb_redis_uptime_seconds` / `puckdb_redis_connected_clients` / `puckdb_redis_used_memory_bytes` / `puckdb_redis_maxmemory_bytes` | Gauge | — | Redis server `INFO` readings (maxmemory 0 = no limit) |
| `puckdb_redis_keyspace_hits_total` / `puckdb_redis_keyspace_misses_total` / `puckdb_redis_expired_keys_total` / `puckdb_redis_evicted_keys_total` | Counter | — | Redis server `INFO` counters since the server started |
| `puckdb_redis_db_keys` / `puckdb_redis_db_expiring_keys` | Gauge | `db` | Keys (with an expiry) per Redis logical database; an emptied database leaves no series |
| `puckdb_db_table_row_count` | Gauge | `table` | Row count per table |
| `puckdb_db_table_size_bytes` | Gauge | `table` | On-disk size per table (heap + indexes + TOAST) |
| `puckdb_db_size_bytes` | Gauge | — | Total database size |
| `puckdb_db_last_updated_timestamp` | Gauge | — | Unix ts of last DB scan |
| `puckdb_data_latest_timestamp` | Gauge | `dataset` | Unix ts of the newest data per dataset (`cmd/metrics_freshness.go`: `nhl_games_final`, `nhl_boxscores`, `nhl_play_by_play`, `nhl_shifts`, `nhl_day_imports`, `nhl_standings`, `yahoo_rosters`, `yahoo_team_summaries`, `yahoo_standings`, `yahoo_transactions`, `news_articles`, `news_events`, `draft_rankings`, `maurice_turns`); a date column counts as 00:00 UTC, an empty dataset has no series, a failed query keeps the last value |
| `puckdb_data_latest_date_info` | Gauge | `dataset`, `date` | Always 1; `date` is the UTC `YYYY-MM-DD` of `puckdb_data_latest_timestamp`, for display (a Grafana date unit would shift a 00:00 UTC date into the viewer's time zone) |
| `puckdb_build_info` | Gauge | `version` | Build version of the running binary |

## Database Schema

PostgreSQL database storing NHL game data and Yahoo Fantasy league data. Two main data domains that link via player matching.

### Data Volume (puckdb_prod, snapshot 2026-04-07)
| Table | Rows | Growth |
|-------|------|--------|
| `shifts` | ~14.5M | Per shift per game |
| `play_events` | ~7.8M | Per play (PBP) per game |
| `game_skater_stats` | ~2.26M | Per game per player |
| `yahoo_team_rosters` | ~1.0M | Per day per roster slot |
| `goal_highlights` | ~400K | Per goal |
| `player_season_totals` | ~319K | Per player per season |
| `standings_snapshots` | ~305K | Per team per day |
| `game_goalie_stats` | ~245K | Per game per goalie |
| `club_skater_stats` | ~76K | Per team per season |
| `games` | ~65K | ~1,300/season |
| `season_rosters` | ~59K | Per season roster slot |
| `yahoo_team_summaries` | ~54K | Per team per day |
| `players` | ~9.6K | Slow (new players only) |

### NHL Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `players` | NHL players (skaters + goalies) | `id` (NHL ID), `yahoo_id`, `first_name`, `last_name`, `team_id`, `position` |
| `seasons` | NHL seasons | `id` (e.g., 20242025), `standings_start`, `standings_end` |
| `franchises` | NHL franchises (historical) | `id`, `full_name`, `team_common_name` |
| `season_teams` | Teams per season (handles relocations) | `season_id`, `team_id`, `franchise_id`, `abbrev`, `division_name` |
| `season_rosters` | Per-season roster snapshots (PK `(season, team_id, player_id)`) | `season`, `team_id`, `player_id`, `position`, `shoots_catches`, `sweater_number`, `height_inches`, `weight_pounds`, `birth_date`, `birth_country` |
| `games` | Individual games | `id`, `season`, `game_type`, `game_date`, `home_team_id`, `away_team_id`, `game_state` |
| `game_broadcasts` | Broadcast feeds per game (PK `(game_id, broadcast_id)`) | `game_id`, `broadcast_id`, `market`, `country_code`, `network`, `sequence_number` |
| `game_coaches` | Coaches per game (PK `(game_id, team_id)`) | `game_id`, `team_id`, `head_coach` |
| `game_officials` | Referees / linesmen (PK `(game_id, role, sequence)`, role ∈ `{referee, linesman}`) | `game_id`, `role`, `sequence`, `name` |
| `game_scratches` | Scratched players (PK `(game_id, player_id)`) | `game_id`, `team_id`, `player_id` |
| `game_three_stars` | 3 Stars of the game (PK `(game_id, star)`, star 1–3) | `game_id`, `star`, `player_id` |
| `game_skater_stats` | Per-game skater stats | `game_id`, `player_id`, `goals`, `assists`, `points`, `toi_seconds`, `shots_on_goal` |
| `game_goalie_stats` | Per-game goalie stats | `game_id`, `player_id`, `saves`, `goals_against`, `save_pctg`, `decision` |
| `play_events` | Play-by-play events (~7.8M rows). PK `(game_id, event_id)`. ~43 columns (one per event variant) | `game_id`, `event_id`, `period`, `type_desc_key`, `x_coord`, `y_coord`, `shooting_player_id`, `goalie_in_net_id`, `assist1_player_id`, `assist2_player_id`, `committed_by_player_id`, `hitting_player_id`, ... |
| `shifts` | Per-shift TOI data (largest table — ~14.5M rows) | `id` (PK), `game_id`, `player_id`, `team_id`, `period`, `start_time`, `end_time`, `duration`, `shift_number`, `event_number`, `event_description` |
| `even_strength_pair_toi` | Per-game shared even-strength TOI between a skater and an on-ice teammate on the same club, both directions stored; derived from `shifts` + box scores and rebuilt per game in one transaction by the shift chart import, read by the projection linemate query and the `get_player_linemates` MCP tool (PK `(game_id, player_id, teammate_id)`, ON DELETE CASCADE from `games`) | `game_id`, `player_id`, `teammate_id`, `shared_toi_seconds` |
| `even_strength_skater_games` | Per-game even-strength TOI and points for one skater; same derivation and rebuild as `even_strength_pair_toi` (PK `(game_id, player_id)`, ON DELETE CASCADE from `games`) | `game_id`, `player_id`, `team_id`, `toi_seconds`, `points` |
| `nhl_day_imports` | One row per NHL day `ImportDayIfChanged` imported (PK `(season, date)`, `season` = full ID like 20232024): SHA-256 of the day's cached input files (path, size, mtime; absent files included) and the importer version; a matching row makes the next import skip the day | `season`, `date`, `importer_version`, `inputs_hash`, `input_files`, `imported_at` |
| `process_players_manifests` | One player manifest per `ProcessPlayersWorkflow` chain with a non-empty boxscore list (an empty list stores nothing; PK `id` = the chain's FirstRunID); the newest 3 are kept, older ones deleted with their players and errors when a new one is created | `id`, `workflow_id`, `player_count`, `players_hash` (sha256 of the ordered rows), `created_at`, `finished_at` |
| `process_players_manifest_players` | The chain's boxscore players, written once (PK `(manifest_id, ordinal)`, UNIQUE `(manifest_id, player_id)`, ordinal ordered by player ID) | `manifest_id`, `ordinal`, `player_id`, `first_name`, `last_name`, `position` |
| `process_players_manifest_errors` | Per-player errors of a chain, recorded from completed batch results (PK `(manifest_id, player_id, stage)`, FK `(manifest_id, player_id)` → `process_players_manifest_players`, stage ∈ `{missing_import, read, upsert, career}`) | `manifest_id`, `player_id`, `stage`, `message` |
| `shootout_attempts` | Shootout attempts (PK `(game_id, sequence)`) | `game_id`, `sequence`, `player_id`, `team_id`, `shot_type`, `result`, `game_winner` |
| `goal_highlights` | Highlight clips per goal (PK `(game_id, event_id)`) | `game_id`, `event_id`, `player_id`, `period`, `time_in_period`, `goals_to_date`, `highlight_clip_id`, `highlight_clip_url`, `discrete_clip_id` |
| `standings_snapshots` | Daily standings per team (PK `(season, date, team_abbrev)` — note `team_abbrev`, not `team_id`) | `season`, `date`, `team_abbrev`, `wins`, `losses`, `ot_losses`, `points`, `division_abbrev`, `division_name`, `conference_abbrev` |
| `club_skater_stats` | Aggregated club skater stats per season (PK `(season, game_type, team_id, player_id)`). Per-club rows: a traded player appears under each club with only that stint's stats; only `regular_season` and `playoffs` are imported | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `goals`, `assists`, `points`, `shots`, `shooting_pctg`, `avg_toi_per_game`, `faceoff_win_pctg` |
| `club_goalie_stats` | Aggregated club goalie stats per season (PK `(season, game_type, team_id, player_id)`). Same per-club and game-type semantics as `club_skater_stats` | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `wins`, `losses`, `overtime_losses`, `goals_against_average`, `save_percentage`, `shutouts`, `toi_seconds` |
| `player_season_totals` | Per-season aggregates per player, **including minor leagues** (PK `(player_id, season, game_type, league_abbrev, sequence)`) | `player_id`, `season`, `game_type`, `league_abbrev`, `sequence`, `team_name`, `games_played`, `goals`, `assists`, `points`, `plus_minus`, `pim` |
| `player_awards` | NHL awards / trophies (PK `(player_id, trophy_name, season)`) | `player_id`, `trophy_name`, `season` |

### NHL Edge Tracking Data (2021-2022 onward)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `edge_skater_stats` | Per-skater Edge summary (PK `(player_id, season, game_type)`) | `top_speed_imperial/metric`, `bursts_over_20`, `total_distance_imperial/metric`, `top_shot_speed_imperial/metric`, `oz/nz/dz_pctg` + percentiles and league avgs |
| `edge_skater_shot_locations` | Shot locations by rink area (PK `(player_id, season, game_type, area)`) | `sog`, `goals`, `shooting_pctg` + percentiles |
| `edge_skater_sog_summary` | SOG summary by location code (PK `(player_id, season, game_type, location_code)`) | `shots`, `goals`, `shooting_pctg` + percentiles and league avgs |
| `edge_goalie_stats` | Per-goalie Edge summary (PK `(player_id, season, game_type)`) | `gaa_value`, `games_above_900_value`, `goal_diff_per_60_value`, `point_pctg_value` + percentiles and league avgs |
| `edge_goalie_shot_location_summary` | Goalie saves by location code (PK `(player_id, season, game_type, location_code)`) | `goals_against`, `saves`, `save_pctg` + percentiles and league avgs |
| `edge_goalie_shot_locations` | Goalie saves by rink area (PK `(player_id, season, game_type, area)`) | `saves`, `save_pctg` + percentiles |
| `edge_team_stats` | Per-team Edge summary (PK `(team_id, season, game_type)`) | `shot_attempts_over_90`, `top_shot_speed_imperial/metric`, `speed_max_imperial/metric`, `bursts_over_22/20`, `total_distance`, `oz/nz/dz_pctg` + ranks and league avgs |
| `edge_team_sog_summary` | Team SOG by location code (PK `(team_id, season, game_type, location_code)`) | `shots`, `goals`, `shooting_pctg` + ranks and league avgs |
| `edge_team_shot_locations` | Team shot locations by area (PK `(team_id, season, game_type, area)`) | `shots`, `shots_rank` |
| `edge_team_zone_time_by_strength` | Zone time by strength code (PK `(team_id, season, game_type, strength_code)`) | `oz/nz/dz_pctg` + ranks |
| `edge_team_shot_differential` | Shot differential by strength (PK `(team_id, season, game_type, strength_code)`) | `for_per_game`, `against_per_game`, `differential_per_game` + ranks |

**Game types:** 1=preseason, 2=regular, 3=playoffs
**Game states:** FUT=future, LIVE=in progress, OFF/FINAL=completed

### Yahoo Fantasy Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `yahoo_leagues` | Fantasy leagues | `id`, `league_key`, `name`, `season`, `num_teams`, `scoring_type` |
| `yahoo_teams` | Fantasy teams in leagues | `league_id`, `id`, `team_key`, `name`, `is_owned_by_current_login` |
| `yahoo_team_rosters` | Daily roster snapshots | `league_id`, `team_id`, `date`, `player_id`, `selected_position` |
| `yahoo_team_managers` | Team managers | `league_id`, `team_id`, `manager_id`, `nickname` |
| `yahoo_team_summaries` | A team's day (PK `(league_id, team_id, date)`): fixed category columns from the daily team stats (`wins` is the goalie-wins category), plus the H2H standings snapshot captured that day, NULL when none was (Yahoo serves current standings only, so a missed day stays a gap); `GetLatestYahooStandingsSnapshotDate` is the league's latest snapshot | `goals` … `blocks`, `standings_captured_at` (file mtime; NULL = no snapshot), `standings_rank`, `standings_playoff_seed`, `h2h_wins`/`h2h_losses`/`h2h_ties`/`h2h_percentage`, `h2h_division_*` |
| `yahoo_team_summary_stats` | Every category of a team summary, any Yahoo stat ID (PK `(league_id, team_id, date, stat_id)`, FK → `yahoo_team_summaries` ON DELETE CASCADE); the import replaces a summary's whole set, so no stale category stays | `stat_id`, `value` (double, NULL for `-`/non-numeric), `raw_value` (Yahoo's text) |
| `yahoo_matchups` | Head-to-head matchups (PK `(league_id, week, team1_id, team2_id)`) | `league_id`, `week`, `team1_id`, `team2_id`, `team1_points`, `team2_points`, `status`, `is_playoffs`, `is_consolation` |
| `yahoo_draft_results` | Draft picks (PK `(league_id, round, pick)`) | `league_id`, `round`, `pick`, `team_id`, `player_id`, `cost` |
| `yahoo_transactions` | Adds, drops, trades (PK `(league_id, transaction_key)`); players are normalized in `yahoo_transaction_players` | `league_id`, `transaction_key`, `type`, `timestamp`, `status` |
| `yahoo_league_stat_categories` | Scoring categories | `league_id`, `stat_id`, `name`, `sort_order` (1 higher / 0 lower is better, NULL unknown), `position_type`, `is_only_display_stat`, `value` (points weight) |
| `yahoo_league_roster_positions` | Roster position config | `league_id`, `position`, `count` |
| `yahoo_league_rule_snapshots` | Versioned normalized league rules (UNIQUE `(season, league_key, rules_hash)`); latest = max `last_seen_at` | `season`, `league_id`, `league_key`, `game_key`, `source` (`yahoo_api`/`temporary_stand_in`), `source_league_key`, `fetched_at`, `rules` (jsonb) |
| `yahoo_league_players` | Draftable pool with league eligibility/status (PK `(league_key, player_id)`) | `season`, `league_id`, `player_key`, `eligible_positions`, `status`, `injury_note`, `fetched_at` |

**Yahoo league identity:** Yahoo league IDs are unique only within one season (game key). The legacy `yahoo_*` tables key by numeric `league_id` alone; the importer refuses to overwrite a `yahoo_leagues` row that belongs to another season. The draft helper tables key by `league_key` + season.

### Player news tables (draft helper)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `news_fetch_state` | Fetch coverage per source and scope (PK `(source_id, scope)`); a failure never clears the last success | `etag`, `last_modified`, `body_hash`, `last_attempt_at`, `last_success_at`, `data_as_of`, `last_error`, `consecutive_failures` |
| `news_articles` | One row per story (UNIQUE `(publisher, external_id)`) | `source_id`, `kind` (`official`/`structured`/`reporting`), `url`, `first_seen_at`, `last_seen_at`, `source_updated_at` |
| `news_article_versions` | Each distinct content of a story (UNIQUE `(article_id, version)`); unprocessed while `processed_at` is NULL. `evidence_text` is the bounded summary; `body` the full text when the source offers it | `content_hash`, `title_fingerprint`, `text_fingerprint`, `title`, `evidence_text`, `body`, `published_at`, `source_updated_at`, `retrieved_at`, `subjects` (jsonb), `team_hints`, `category_hint` |
| `news_mentions` | Player references per version (PK `(version_id, ordinal)`) | `role` (`subject`/`mentioned`), `resolution` (`resolved`/`ambiguous`/`unresolved`), `method`, `nhl_player_id`, `yahoo_player_id`, `candidates` (jsonb) |
| `news_incidents` | Incident candidates: one per player, category and time window, however many reports repeat it | `nhl_player_id`, `yahoo_player_id`, `category`, `first_reported_at`, `last_reported_at` |
| `news_incident_evidence` | Versions behind an incident (PK `(incident_id, version_id)`) | `publisher`, `kind`, `reported_at`, `relation` (`independent`/`syndicated`/`same_publisher`/`revision`) |
| `news_extractions` | LLM extraction per version and extractor (UNIQUE `(version_id, extractor_key)`; key = provider/model/prompt/schema version) | `input_hash` (cache), `status` (`pending`/`succeeded`/`invalid`/`failed`), `attempts`, `last_error`, `raw_output`, `issues` (jsonb), tokens, `cached_from_id`, `reconciled_at` |
| `news_events` | Validated events; facts immutable, new details = new event | `nhl_player_id`, `yahoo_player_id`, `event_type`, `report_status` (`confirmed`/`reported`/`rumor`), `effective_from`, `duration_kind`/`_games`/`_days`/`_until`, `change_field`/`_from`/`_to`, `lifecycle` (`active`/`superseded`/`retracted`/`resolved`), `superseded_by`, `incident_id`, `needs_review`, `review_reason` |
| `news_event_evidence` | Versions behind an event per extraction (PK `(event_id, version_id, extraction_id, relation)`) | `relation` (`supports`/`contradicts`/`retracts`/`resolves`/`withdraws`), `quotes` (jsonb), `publisher`, `kind`, `reported_at` |
| `news_event_transitions` | Audit trail of every event creation and lifecycle change | `event_id`, `from_lifecycle`, `to_lifecycle`, `version_id`, `extraction_id`, `reason`, `at` |
| `news_extraction_evaluations` | Runs of the labeled corpus per extractor; the latest passing run on the current corpus version gates automatic effects | `extractor_key`, `corpus_version`, `passed`, `metrics` (jsonb) |

### News adjustment tables (draft helper)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `news_adjustment_overrides` | Manager overrides; never deleted, ended by `reset_at` or `expires_at` | `id`, `player_key`, `league_key`, `kind` (`missed_games`/`input`/`exclude_event`), `event_id`, `scenario`, `input`, `value`, `reason`, `created_at` |
| `news_adjustment_runs` | One adjustment of a baseline projection snapshot (UNIQUE `adjustment_id`) | `policy` (jsonb), `baseline_snapshot_id`, `league_key`, `as_of`, `season`, `overrides` (as of `as_of`), `coverage_warnings` |
| `news_adjustment_events` | Event versions a run saw (PK `(run_id, event_id)`) | `version`, `incident_id`, `outcome`, `reason`, `scenarios`, `event` (jsonb) |
| `news_adjustment_scenarios` | Adjusted projection snapshot per scenario (PK `(run_id, scenario)`) | `snapshot_id` → `projection_snapshots` |
| `news_adjustment_players` | Per-player explanation (PK `(run_id, player_key)`) | `adjustment` (jsonb) |

### Draft ranking tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `draft_ranking_snapshots` | One successful ranking refresh of a league; immutable, latest by `as_of` is served | `season`, `league_id`, `league_key`, `identity`, `rules_hash`, `projection_snapshot_id`, `adjustment_run_id`, `as_of`, `meta` (jsonb) |
| `draft_ranking_players` | Every pool player of a snapshot with each scenario placement (PK `(snapshot_id, player_key)`) | `baseline_rank`, `player` (jsonb) |
| `draft_ranking_refreshes` | Refresh attempts per league (UNIQUE `(run_id, season, league_id)`) | `status` (`running`/`succeeded`/`failed`/`canceled`), `state` (issue code), `error`, `snapshot_id`, `started_at`, `finished_at` |
| `draft_sessions` | Full-league-key live board and manual overlay | `state_version`, `sync_version`, `board`, `draft_status`, freshness/error timestamps, recommendation safety |
| `draft_session_observations` | Every Yahoo watch attempt for capability measurement | duration, authority, counts, change, error class |
| `draft_session_events` | Versioned board/manual changes; identical polls add no event | `state_version`, `kind`, `details`, `created_at` |
| `draft_shortlist` | Persisted Maurice shortlist per full league key | `league_key`, `player_key`, `created_at` |
| `draft_recommendation_runs` | Replayable deterministic draft advice input/output | session/ranking/projection/rule versions, strategy, numeric reasons, latency |

### Maurice (LLM chat) tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `app_users` | Local projection of a login provider identity on one API instance (UNIQUE `(instance, auth_provider, auth_subject)`; users from before migration 000028 belong to `puckdb`; username/display name/email are mutable snapshots) | `id` (uuid), `instance`, `auth_provider` (`authentik`/`cloudflare`), `auth_subject` (`X-authentik-uid` or JWT `sub`), `username`, `display_name`, `email` (JWT `email`, or `X-authentik-email`; NULL when the provider gives none), `first_seen_at`, `profile_updated_at`, `disabled_at`, `team_offer_seen_at` (first save or dismissal of the team picker offer, NULL before), `member_yahoo_email` (the members file's `yahoo_email` for the login email at the last login, NULL: none; read by `unlink-team`, which never reads the file) |
| `app_sessions` | One PuckDB application session; only a keyed hash of the cookie is stored | `user_id`, `session_key_hash` (UNIQUE), `started_at`, `last_activity_at`, `ended_at` (idle expiry) |
| `app_user_yahoo_managers` | Links a user to a Yahoo account, the manager guid (`internal/yahoolink`): the user's teams are every `yahoo_team_manager_guids` row with that guid, every season. Synced on every authenticated request from the guids the rows matching the login email or the members file's Yahoo email resolve to, or added by `puckdb user link-team` for a team whose managers all resolve to one guid; backfilled by migration 000032 from such teams' team links (PK `(user_id, guid)`, guid never empty nor `--…`, FK → `app_users` ON DELETE CASCADE) | `user_id`, `guid`, `source` (`email`/`manual`/`self`, as below), `linked_at` |
| `app_user_yahoo_teams` | Links a user to one Yahoo team they manage, several per user (`internal/yahoolink`), the fallback for a manager row resolving to no guid (a new manager whose guid Yahoo hides, a league without emails) or a co-managed team; synced on every authenticated request from the `yahoo_team_managers.email` matches (case-insensitive) that resolve to no guid (the others get an `app_user_yahoo_managers` link), or added by hand (`puckdb user link-team`) for a Yahoo email that differs from the login email; `league_id`/`team_id` are the legacy numeric Yahoo identifiers, not `league_key`. Named in Maurice's system prompt, limited to `--maurice-yahoo-leagues` (PK `(user_id, league_id, team_id)`, FK → `yahoo_teams(league_id, id)` ON DELETE CASCADE) | `user_id`, `league_id`, `team_id`, `source` (`email`/`manual`/`self` = the user's own pick in the team picker; a manual or self link always survives a later resync, and `link-team` promotes either other source to manual), `linked_at` |
| `app_user_yahoo_team_rejections` | Teams a user deselected in the team picker (`yahoolink.Picker`): hidden from the user's teams whatever link reaches it (a guid link's other teams stay) and given no email team link by the resync; removed when the user picks the team again, by `puckdb user link-team`, or by `unlink-team` once neither the login email nor the member Yahoo email matches one of the team's managers or resolves to a guid one of them resolves to (PK `(user_id, league_id, team_id)`, FKs → `app_users` and `yahoo_teams(league_id, id)` ON DELETE CASCADE) | `user_id`, `league_id`, `team_id`, `rejected_at` |
| `maurice_conversations` | One row per chat session, owned by a user (every query is scoped by `user_id`, so by the user's instance). Deleting sets `deleted_at` (hidden from the owner, rows and usage kept) | `id` (uuid), `user_id`, `title`, `created_at`, `updated_at`, `deleted_at` |
| `maurice_turns` | One prompt and all the work behind its answer. UNIQUE `(user_id, idempotency_key)`, one `running` turn per conversation (partial unique index); a running turn older than the stale cutoff is failed as `abandoned` | `conversation_id`, `turn_number`, `idempotency_key`, `request_hash`, `status` (`running`/`succeeded`/`failed`/`cancelled`), `started_at`, `completed_at`, `error_class` (bounded label; `empty_answer`: no answer text even after the retries), `tool_names` (jsonb array: each tool appended as its call starts while running, duplicates kept; `mauriceTurn.toolsUsed` of a turn that did not succeed) |
| `maurice_messages` | Messages of a turn (UNIQUE `(turn_id, message_number)`). `role` ∈ `{system, user, assistant, tool}`. Assistant calls and tool-result links are normalized through `maurice_tool_calls`; transcript reads rebuild provider-neutral `llm.ToolCall` values. The replayable transcript includes only messages of `succeeded` turns; failed/cancelled turns keep their exact prompts | `id` (uuid), `conversation_id`, `turn_id`, `message_number`, `role`, `content`, `result_tool_call_id`, `created_at` |
| `maurice_llm_calls` | One provider request: `chat_round`, `forced_final`, or `title_generation` (no turn). Token counts are provider-reported and NULL when not reported | `turn_id`, `conversation_id`, `call_kind`, `round_number`, `provider`, `model`, `provider_request_id`, `status`, `finish_reason`, `started_at`, `completed_at`, `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `error_class`, `system_prompt`, `instruction`, `tool_definitions` |
| `maurice_llm_call_messages` | Ordered message references of each call's request (system prompt excluded), so every prompt can be rebuilt without copying content (PK `(llm_call_id, input_number)`) | `llm_call_id`, `input_number`, `message_id` |
| `maurice_tool_calls` | Each tool call a response requested, linked to its assistant message when retained (UNIQUE `(llm_call_id, sequence_number)` and `(message_id, sequence_number)`). `arguments_raw` is lossless and may be malformed; nullable `arguments` is its queryable JSONB projection. Rows without call telemetry retain transcript-only calls | `turn_id`, `llm_call_id`, `message_id`, `sequence_number`, `provider_tool_call_id`, `tool_type`, `tool_name`, `arguments_raw`, `arguments`, `result`, `status` (`succeeded`/`tool_error`/`parse_error`/`cancelled` = never ran; NULL = transcript-only), `started_at`, `completed_at` |

A turn's messages, calls, call-message links and tool calls commit in one transaction after the tool loop (`FinishTurn`); no transaction stays open across model calls. Prompts and tool payloads are kept forever (deleting a conversation only hides it); legacy conversations without an owner were deleted by migration 000022. The only analytics API is the read-only, admin-only reporting fields above (metadata and aggregates; the conversation detail fields also return message and payload content, on explicit request); there is no export or erase capability.

### Simulation tables

All created by migration 000001 (window columns by 000002); per-type `sim_transactions` columns and the rules behind them on wiki `puckdb/simulation.md`.

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `sim_pools` | One simulation pool and its immutable config; `workflow_id` is generated as `'sim-pool-' \|\| id` | `season`, `status` (`draft` until the workflow ends, then `complete`/`paused`/`cancelled`; `running` is allowed but never written), `sim_date`, `num_teams`, `roster_c` … `roster_ir`, `categories`, `waiver_days`, `draft_rounds`, `max_llm_cost_usd_per_pool`, `total_llm_cost_usd`, `stop_after`, `max_season_days`, `start_date`/`end_date` (NULL = season bounds), `record_full_messages` |
| `sim_agents` | One LLM-managed team per row | `pool_id`, `draft_position`, `provider`, `model`, `strategy`, `timeout_seconds`, `temperature`, `api_base`, `max_tokens`, `team_name`, `notes` (≤ 50,000 bytes) |
| `sim_rosters` | Current roster (PK `(pool_id, agent_id, player_id)`, UNIQUE `(pool_id, player_id)`) | `slot`, `acquired_at`, `acquired_via` (`draft`/`free_agent`) |
| `sim_waiver_priority` | Waiver order (PK `(pool_id, agent_id)`; 1 = first) | `priority` |
| `sim_waiver_claims` | Waiver claims (one pending claim per pool, agent and player) | `player_id`, `drop_player_id`, `filed_date`, `process_date`, `status` (`pending`/`won`/`lost`/`cancelled`), `resolved_at` |
| `sim_transactions` | Audit log of every decision; typed columns filled per `type` (`draft_pick`, `add`, `claim`, `drop`, `lineup_set`, `pass`, `error`, `cost_cap_reached`, and the `daily_turn_done` idempotency marker) | `pool_id`, `agent_id`, `date`, `type`, `player_id`, `reasoning`, `round`, `pick`, `drop_player_id`, `error_kind`, `error_detail`, `cost_usd`, `cap_usd` |
| `sim_lineup_moves` | Moves of a `lineup_set` transaction (PK `(transaction_id, sequence)`) | `player_id`, `from_slot`, `to_slot`, `displaced_player_id` |
| `sim_agent_daily_player_stats` | Per-player daily contribution (source of truth; PK `(pool_id, agent_id, date, player_id, category)`) | `value`, `goalie_ga`, `goalie_toi_seconds` |
| `sim_agent_daily_stats` | Per-agent daily rollup (PK `(pool_id, agent_id, date, category)`) | `value`, `goalie_ga`, `goalie_toi_seconds` |
| `sim_agent_totals` | Season totals (PK `(pool_id, agent_id, category)`) | `value`, `goalie_ga`, `goalie_toi_seconds` |
| `sim_standings` | Daily roto standings (PK `(pool_id, date, agent_id, category)`) | `value`, `roto_points` (fractional on ties) |
| `sim_agent_turns` | One team-name, draft or daily LLM turn (unique per pool, agent, phase, date and pick) | `phase` (`team_name`/`draft`/`daily`), `sim_date`, `pick_number`, `status` (`ok`/`errored`/`skipped`), `skip_reason`, `provider`, `model`, `rounds`, token counts, `cost_usd`, `latency_ms`, `final_text` |
| `sim_agent_turn_rounds` | Per-round usage of a turn (PK `(turn_id, round_index)`) | `assistant_text`, token counts, `latency_ms` |
| `sim_agent_turn_messages` | Full transcript of a turn, written only when `record_full_messages` is on (PK `(turn_id, ordinal)`). Assistant calls and tool results link to normalized `sim_agent_tool_calls` rows | `role`, `content`, `result_tool_round_index`, `result_tool_sequence` |
| `sim_agent_tool_calls` | Every tool call of a turn (PK `(turn_id, round_index, sequence)`), optionally linked to its retained assistant message. `arguments_raw` preserves malformed/provider-specific text; nullable `arguments` is its queryable JSONB projection | `message_ordinal`, `provider_tool_call_id`, `tool_type`, `tool_name`, `recovered_name`, `arguments_raw`, `arguments`, `result`, `outcome` (`accepted`/`parse_error`/`validation_rejected`/`unknown_tool`/`unhandled`), `failure_reason`, `applied_transaction_id` |

### Views

Reporting views (definitions in migrations): `skater_season_stats`, `skater_recent_stats`, `goalie_season_stats`, `goalie_recent_stats`, `yahoo_roster_players`, `yahoo_roto_standings`, `yahoo_season_team_totals`, `yahoo_team_manager_guids` (each `yahoo_team_managers` row's Yahoo account: its guid when real, else the one real guid other rows with its email carry, compared case-insensitively; a row resolving to none is absent), `app_user_usage` (per user, with its `instance`: `puckdb_session_count`, last session, turns, LLM calls, token sums including title generation, `active_seconds` = turn durations + title calls, i.e. time spent processing prompts; lifetime, deleted conversations included, unreported tokens summed as 0, so the filtered reporting overview does not read it).

### Key Relationships

```
players.id ←──── game_skater_stats.player_id
players.id ←──── game_goalie_stats.player_id
players.yahoo_id ───→ yahoo_team_rosters.player_id (matched via enrichment)

seasons.id ←──── season_teams.season_id
seasons.id ←──── games.season

franchises.id ←──── season_teams.franchise_id

games.id ←──── game_skater_stats.game_id
games.id ←──── game_goalie_stats.game_id

yahoo_leagues.id ←──── yahoo_teams.league_id
yahoo_teams.(league_id, id) ←──── yahoo_team_rosters.(league_id, team_id)
```

### Common Queries

```sql
-- Player season totals
SELECT p.first_name, p.last_name, SUM(s.goals), SUM(s.assists)
FROM players p
JOIN game_skater_stats s ON p.id = s.player_id
JOIN games g ON s.game_id = g.id
WHERE g.season = 20242025 AND g.game_type = 2
GROUP BY p.id;

-- Team roster on a date
SELECT p.first_name, p.last_name, r.selected_position
FROM yahoo_team_rosters r
JOIN players p ON p.yahoo_id = r.player_id
WHERE r.league_id = 12345 AND r.team_id = 1 AND r.date = '2024-12-01';
```

## Maurice (AI Chat)

Interactive REPL for querying hockey data via natural language. Connects to an LLM and uses MCP tools to query PostgreSQL.

### Architecture

| Component | File | Purpose |
|-----------|------|---------|
| CLI entry | `cmd/maurice.go` | REPL loop, `/model` `/history` `/load` `/new` commands |
| Service | `internal/maurice/service.go`, `turnrun.go` | Chat orchestration, tool call loop (max 25 rounds, then one forced answer without tools), conversation persistence; an answer with no text is retried without tools with a nudge, keeping the tool results and never rerunning tools, up to `maxFinalAnswerAttempts` (3) attempts in all, each empty attempt logged with its stop reason (`finalanswer.go`), then fails the turn as `empty_answer`; history replay leaves out a stored assistant message with no text and no tool calls (older turns stored such replies); the `--maurice-max-history` cap cuts the replay at the start of a turn (it always opens on a user message, the current prompt at worst, `capHistory` in `history.go`); turns run detached from the request (`StartChat`/`GetTurn` for submit-and-poll) |
| Prompt | `internal/maurice/prompt.go` | System prompt (instructs LLM to query DB, not guess) |
| LLM clients | `internal/llm/client.go`, `internal/llm/anthropic.go` | OpenAI-compatible (Ollama/OpenAI) and Anthropic clients |
| Provider detection | `internal/llm/provider.go` | Auto-detects Anthropic vs OpenAI-compatible from API key/URL |
| MCP integration | `internal/mcp/` | Connects to puckdb MCP server for database tool calls |
| Persistence | `~/.puckdb/maurice.db` | SQLite for the REPL (single user `maurice.LocalUserID`; turns and transcript, no LLM-call usage); the API uses PostgreSQL (`maurice.NewPgDB`) |

MCP discovery uses partial service: when at least one configured server is
healthy, Maurice advertises and routes only that server's discovered tools and
returns a warning with the chat response (the REPL prints it). Partial results
are not cached, so failed servers are retried on the next chat. Each chat keeps
the immutable route snapshot matching its advertised tools, even while another
chat retries discovery. If every configured server fails discovery, the chat
fails before calling the model.

### Configuration

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `maurice-model` | `PUCKDB_MAURICE_MODEL` | `claude-sonnet-5-5` | Model ID; must be in `mauriceModels` (`cmd/maurice.go`), which also picks the provider. Unknown IDs fail startup |
| `ollama-base-url` | `PUCKDB_OLLAMA_BASE_URL` | `http://localhost:11434/v1` | Ollama (OpenAI-compatible) endpoint |
| `anthropic-api-key` | `PUCKDB_ANTHROPIC_API_KEY` | (empty) | Required for Anthropic models |
| `openai-api-key` | `PUCKDB_OPENAI_API_KEY` | (empty) | Required for OpenAI models |
| `maurice-config` | `PUCKDB_MAURICE_CONFIG` | `~/.puckdb/maurice.yaml` | Path to Maurice config file |
| `maurice-max-tokens` | `PUCKDB_MAURICE_MAX_TOKENS` | `4096` | Max response tokens |
| `maurice-max-history` | `PUCKDB_MAURICE_MAX_HISTORY` | `50` | Conversation history depth |
| `maurice-max-tool-rounds` | `PUCKDB_MAURICE_MAX_TOOL_ROUNDS` | `25` | Max tool-call rounds per message |
| `maurice-members` | `PUCKDB_MAURICE_MEMBERS` | (empty) | `api` only: path of the instance's members file (login email → first name, Yahoo email); empty: none; a malformed file fails startup |
| `maurice-yahoo-leagues` | `PUCKDB_MAURICE_YAHOO_LEAGUES` | (empty) | Comma-separated numeric Yahoo league IDs whose teams Maurice names in the system prompt and offers in the team picker (empty: every league); a malformed list fails `api` startup |

### REPL Commands

| Command | Action |
|---------|--------|
| `/model` | Show current model + numbered model menu |
| `/model <number>` | Switch to the model at that menu position (rebuilds LLM client) |
| `/new` | Start new conversation |
| `/history` | List recent conversations |
| `/load <id>` | Resume a conversation |
| `/quit` | Exit |

### Prompt format

```
maurice (<model-name>) >
```

## Testing Guidelines

Always run the full test suite (`go test ./...`) and verify 100% pass rate before committing any changes. Do not commit if any tests fail.

### Database-backed tests

Tests that need a real PostgreSQL read `PUCKDB_TEST_PG_URL` through `internal/fixtures/pgtest` (except `internal/database`, see the package table) and skip when it is unset. Point it at a dedicated throwaway database, as a `postgres://` URL, whose name contains `test` — the harness refuses any other, then migrates it and each package truncates the tables it names with `CASCADE` (so tables referencing them are emptied too), so run packages one at a time (`go test -p 1 ./...`):

```bash
docker run -d --rm --name puckdb-test-pg -e POSTGRES_USER=puckdb -e POSTGRES_PASSWORD=foo \
  -e POSTGRES_DB=puckdb_test -p 15433:5432 postgres:16-alpine
PUCKDB_TEST_PG_URL='postgres://puckdb:foo@localhost:15433/puckdb_test?sslmode=disable' go test ./internal/maurice/
```

The simulation's `integration` (mocked LLM; PostgreSQL, Redis, Temporal dev server) and `livellm` (real Ollama models) tests in `internal/worker/simulation/` are not compiled by `go test ./...`; their `TestMain` refuses a URL `pgtest.CheckURL` rejects, then migrates the `PUCKDB_TEST_PG_URL` database down before and after the suite. Commands and variables: wiki `puckdb/simulation.md` > "Tests".

`internal/maurice/dbcontract_test.go` is the shared persistence contract for the Maurice `DB` implementations; SQLite runs it in-memory on every `go test`, PostgreSQL runs it only with the env var set. Any behaviour change to one adapter must keep both passing.

### Serialization Error Handling

When testing code that serializes/deserializes domain objects:

1. **Never ignore marshal/unmarshal errors** - Use `require.NoError(t, err)` instead of `_, _ :=`
2. **Use realistic test data** - Types like `nhl.Position` and `nhl.Season` have custom marshalers that require valid values
3. **Understand mock vs storage patterns**:
   - **Mock returns**: Pass structs directly, no serialization validation
   - **Storage pre-population**: Goes through JSON round-trip, all fields must be valid

## References

- [Yahoo Fantasy Sports API Guide](https://developer.yahoo.com/fantasysports/guide/)
- [OAuth2 Example](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)
