# Draft helper: rankings API, CLI and exports

`internal/draftrank` serves each league's draft rankings to GraphQL, the CLI
and its CSV/JSON exports through one `Service` and one view function
(`Snapshot.View`). All three read the same stored snapshots, so the same
league, filters and snapshot give identical values and ranks. Nothing on the
read path ranks, adjusts or calls an LLM: rankings are computed by an
explicit refresh and stored.

## Snapshots

A **snapshot** is one successful refresh of one league
(`draft_ranking_snapshots`, migration `000010`). The refresh:

1. loads the league's latest rules (`draft.LoadSnapshot`) and draftable pool
   (`draft.LoadPool`);
2. builds the league-neutral baseline projection of the pool
   (`projection.Repository.BuildSnapshot`, as of the refresh day, so every
   refresh of a day reuses one stored projection);
3. applies the validated news events and manager overrides known at the
   refresh time (`newsadjust.Apply`, default policy) and stores the run
   (`newsadjust.Repository.SaveRun`). It reads stored extractions only; LLM
   extraction runs in the news refresh, never here;
4. ranks the baseline and each news scenario (conservative, base,
   optimistic) with the single ranking model, `draft.BuildRanking`, under the
   same rules, pool and options;
5. stores the snapshot and every player's placement in each scenario in one
   transaction, then prunes the league to its newest
   `--draft-keep-snapshots` (default 10) snapshots.

A snapshot records its `identity` (a hash of every scenario's ranking version
and the adjustment ID), the ranking version per scenario, the rules hash and
fetch time, the pool's oldest fetch time, the projection snapshot (ID, model
version, source hash, as-of, last game date), the adjustment run (ID, policy
version, calibration label, alerts and load warnings), the ranking options and
assumptions, the coverage of every enabled news source at refresh time, and
what it could not provide (`unavailable`).

Snapshots are immutable. Readers serve a league's latest snapshot by as-of
time; a client that pages through rankings passes the snapshot ID it got on
the first page (`snapshotId`, `--draft-snapshot`) so a refresh in between
cannot mix versions. A newer snapshot is then reported as
`NEWER_SNAPSHOT_AVAILABLE`.

### Refresh and concurrency

`RefreshDraftRankingsWorkflow` (workflow ID `refresh-draft-rankings`, mutation
`refreshDraftRankings`, sync step `refresh-draft-rankings`, group `draft` =
news + rankings) refreshes the requested leagues, or the season's leagues in
`seasons.yaml`, one at a time. It follows the other long-running workflows:
`cancelRefreshDraftRankings`, `refreshDraftRankingsResult` and
`refreshDraftRankingsProgress` (one bar per league).

- **Concurrent refreshes.** The workflow ID is fixed and started with
  `WorkflowExecutionErrorWhenAlreadyStarted`, so a second start while one runs
  is rejected. Snapshot writes of one league are also serialized with a
  PostgreSQL advisory lock, and a retried activity reuses its attempt row
  (`draft_ranking_refreshes`, unique per run and league).
- **Failures keep the last snapshot.** A snapshot becomes visible only when
  its transaction commits. A failed, canceled or interrupted refresh stores
  nothing, and the league keeps serving its previous snapshot with the
  failure reported next to it (`REFRESH_FAILED`, `REFRESH_CANCELED`,
  `REFRESH_INTERRUPTED`). Other leagues of the run still refresh.
- **Known failures are not retried.** Missing rules, pool or projections,
  unsupported scoring and ranking errors are recorded as the attempt's outcome;
  only internal (database) errors are retried by Temporal (3 attempts).
- **Cancellation.** The refresh heartbeats between steps; a cancel stops it at
  the next step, the attempt is recorded as canceled, and a cleanup activity
  marks any attempt the worker could not finish.

Refresh options: bench policy (`INCLUDED`/`EXCLUDED`), workload cap policy
(`PER_PLAYER`, required for leagues with `max_games_played` or
`max_goalie_starts`, see `draft-ranking.md`) and a nonnegative uncertainty
penalty. On the CLI: `--draft-bench-policy`, `--draft-workload-caps`,
`--draft-uncertainty-penalty`, `--draft-leagues`.

## States

Every response carries a `status` and a list of `issues` with machine-readable
codes, so a consumer never has to infer a state from missing data.

| Status | Meaning |
|---|---|
| `READY` | A snapshot is served (issues may still apply) |
| `NOT_COMPUTED` | No refresh has run for the league |
| `REFRESHING` | The league's first refresh is running |
| `FAILED` | No snapshot, and the latest refresh failed, was canceled or was interrupted |

| Issue | When |
|---|---|
| `MISSING_RULES` | No rules imported for the league (refresh failure, or reported directly for a league without a snapshot) |
| `UNSUPPORTED_SCORING` | The rules cannot be scored (unknown scoring type, missing direction or weight, unsupported bonus) |
| `MISSING_POOL` | The league's draftable pool is empty |
| `MISSING_PROJECTIONS` | A pool player lacks a projection for a scoring stat (e.g. a rookie without history or override) |
| `RANKING_FAILED` | The ranking model refused the inputs (unsupported roster slot, unconfirmed workload cap, kind conflict) |
| `INTERNAL_ERROR` | A database or consistency failure |
| `NEWS_ADJUSTMENTS_UNAVAILABLE` | The snapshot has the baseline ranking only: the season's dates are not imported, or the adjustment failed |
| `NEWS_SOURCE_STALE` / `NEWS_SOURCE_FAILING` / `NEWS_SOURCE_MISSING` | A news source was not current when the snapshot was built |
| `PROVISIONAL_RULES` | The rules came from a temporary stand-in league |
| `STALE_SNAPSHOT` / `STALE_POOL` | The snapshot or its pool is older than `--draft-stale-after` hours (default 24) |
| `OVERRIDES_CHANGED` | An override of the league was created, reset or expired after the snapshot; refresh to apply it |
| `RULES_CHANGED` | The league's rules were imported again with changes after the snapshot (e.g. real rules replacing a stand-in); refresh to rank under them |
| `NEWER_SNAPSHOT_AVAILABLE` | A pinned snapshot is no longer the latest |
| `SCENARIO_UNAVAILABLE` | The requested scenario is not in the snapshot; the baseline is served |
| `NOT_COMPUTED`, `REFRESH_RUNNING`, `REFRESH_FAILED`, `REFRESH_CANCELED`, `REFRESH_INTERRUPTED` | Refresh state |

Each refresh attempt (`refresh`) also carries its state, failure code, error,
start and finish times and the snapshot it produced.

## Views: filters, search, sort and pagination

A view never recomputes a value or renumbers a rank.

- **Scenario**: `BASELINE`, `CONSERVATIVE`, `BASE`, `OPTIMISTIC`; `BASE` by
  default when the snapshot has news scenarios, else `BASELINE`. Each row also
  lists its placement in every scenario, its baseline rank and `rankChange`
  (baseline rank minus served rank).
- **Positions**: `C`, `LW`, `RW`, `D`, `G` with OR semantics; a C/LW player
  matches `C,LW` once. Overall and position ranks come from the whole pool.
  `positionRank` is the best rank among the filtered positions (all eligible
  positions when unfiltered).
- **Player keys**: restricts the view to those players; `draftPlayerComparison`
  is this view with every row's placements and explanations.
- **Search**: every word must appear in the name, team or player key; case and
  accents are ignored (`stutzle` finds Stützle).
- **Sort**: `OVERALL_RANK` (default), `POSITION_RANK`, `NAME`, `TEAM`,
  `SCORE`, `VALUE`, `ADJUSTED_VALUE`, `UNCERTAINTY`, `TIER`, `BASELINE_RANK`,
  `RANK_CHANGE`. Ranks, tiers, names and uncertainty sort ascending and
  scores, values and rank changes descending unless a direction is given. Ties
  fall back to overall rank, then player key, so every order is total and pages
  never overlap.
- **Pagination**: `offset` and `limit`. GraphQL defaults to 50 rows and caps a
  page at 1000 (the page reports the limit used); the CLI returns every
  matching row unless `--draft-limit` is set.

## GraphQL

Schema: `internal/graph/draft.graphqls`.

| Field | Returns |
|---|---|
| `draftLeagues(season)` | Every league with imported rules or configured in `seasons.yaml`: rules (scoring format, categories with directions and weights, roster slots, provenance), status, latest snapshot and refresh, issues |
| `draftRankings(input)` | One page: league, status, snapshot (versions, freshness, news coverage, options, assumptions, unavailable parts), refresh, issues and rows |
| `draftPlayerComparison(input)` | The named players of one snapshot, with every scenario placement, per-category contributions and explanations, and news evidence |
| `draftOverrides(leagueKey, playerKey, includeInactive)` | Overrides with their state now (active, expired, reset) |
| `createDraftOverride(input)` / `resetDraftOverride(id, reason)` | Manage overrides; they take effect at the next refresh |
| `refreshDraftRankings(input)` / `cancelRefreshDraftRankings` / `refreshDraftRankingsResult` / `refreshDraftRankingsProgress` | The refresh workflow |

A row holds the player (keys, name, team, eligible positions, Yahoo status and
injury note), the served placement (overall and position ranks, tier, official
and adjusted score, replacement value, value, adjusted value, uncertainty,
per-category projected values and contributions with explanations), every
scenario placement, and the news adjustment: reasons with dated, attributed
evidence (publisher, URL, quote, report and retrieval times, age), effects per
scenario (missed games, availability, ice time, power play, goalie starts,
team), changed stats per scenario, applied overrides with the values they
replaced, assumptions and alerts.

**Access.** The draft fields follow the rest of the API: requests are
authenticated by the Authentik forward-auth proxy in front of the server, and
only the admin mutations carry `@admin`. `createDraftOverride` records the
authenticated user (`X-authentik-username`) as the override's author.

## CLI

```bash
puckdb draft rankings --draft-league 465.l.1001 --draft-positions C,LW --draft-format csv
puckdb draft rankings --draft-league 1002 --draft-season 2026 --draft-search "hughes" --draft-sort value
puckdb draft rankings --draft-league 465.l.1001 --draft-players 465.p.1,465.p.2 --draft-format json
puckdb sync refresh-draft-rankings --draft-workload-caps
puckdb sync draft                       # news refresh, then rankings
```

`--draft-league` takes a league key or a numeric league ID (with
`--draft-season`, default the current season). Other flags: `--draft-scenario`,
`--draft-sort`, `--draft-direction`, `--draft-offset`, `--draft-limit`,
`--draft-snapshot`, `--draft-output` and `--draft-stale-after`. Formats:

- `table`: a header with the league, snapshot, scenario, versions and every
  issue, then one aligned line per player (values rounded for display);
- `csv`: one line per player naming the snapshot ID, identity and scenario,
  with full-precision numbers and a projected/value column pair per scoring
  category;
- `json`: the service page exactly as the API computes it.

A league without a snapshot (`NOT_COMPUTED`, `REFRESHING`, `FAILED`) makes
the command exit non-zero after printing its status and issues, so a scripted
export never succeeds with an empty file. CSV rows cannot carry the page's
status and issues, so the CSV export also writes them to standard error.

## Not done here

- **Category preferences.** `draft.RankingOptions.CategoryWeights` (punting a
  category) is not exposed by the refresh input yet.
- **Rookie projections.** A pool player without NHL history still fails the
  refresh with `MISSING_PROJECTIONS`; there is no stored manual/imported
  projection override yet, so the live 2026 pools will need one before they
  rank.
- **Per-request staleness of news.** News freshness is the coverage at refresh
  time; a source that goes stale later shows up as `STALE_SNAPSHOT` once the
  snapshot ages past `--draft-stale-after`.
