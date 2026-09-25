# Draft helper: league rules and player pool

The draft helper ranks players per league, so it needs each league's actual
rules and a complete, current player pool. This note describes what the Yahoo
sync stores for that, how versions and identities work, and what is not yet
verified.

## League rules

`ImportYahooLeague` (and the temporary `ImportYahooStandInLeague`) now read
`league.xml` straight from storage rather than through the Redis gob cache:
an entry encoded before `store.Settings` gained a field would otherwise decode
with that field zeroed and silently drop stat directions or points weights.

From the settings resource the importer stores:

- `yahoo_league_stat_categories`: besides name/abbr/group, the Yahoo
  `sort_order` (1 = higher is better, 0 = lower is better, NULL = Yahoo did not
  say — never guessed), the scoring `position_type`, `is_only_display_stat`
  (set when Yahoo marks the stat display-only, either on the stat or for every
  position type), and `value`, the points weight from `<stat_modifiers>`
  (previously always NULL).
- `yahoo_league_rule_snapshots`: one row per distinct version of the league's
  normalized rules (`draft.Rules`, JSON) with the league key, game key,
  source (`yahoo_api` or `temporary_stand_in`), the league the settings were
  read from, and `fetched_at` (the settings file's modification time, i.e.
  when Yahoo served it). Re-importing identical rules only moves `fetched_at`
  and `last_seen_at`; any change inserts a new version. The latest version of
  a league in a season is the one seen last.

`draft.Rules` keeps every `<settings>` element: modeled ones (categories,
roster slots, draft type/time/pick time) are structured, every other leaf
element is kept verbatim in `settings`, and nested elements the model ignores
are listed in `unmodeledSettings`. Game or start caps, goalie minimums, keeper
and traded-pick rules therefore appear in the comparison exactly as Yahoo
names them, even though the model does not interpret them yet.

`draft.ScoringFor` is the gate rankings must go through. It fails with
`ErrMissingScoringInputs`, listing every problem, when the scoring type is
unknown, no stat scores, a category league has a stat without a direction, or
a points league has a stat without a weight or with a bonus. It never falls
back to the Crapettes categories the pool simulator hard-codes. Stand-in rules
pass but are marked `Provisional`.

## Player pool

For every league that calls the Yahoo API, the season sync downloads the
league players collection (`/league/<key>/players;start=N;count=25`, no status
filter, so free agents, waiver players, rostered players and rookies without
NHL history are all included) one throttled page per activity into a
directory of its own (`seasons/<Y>/yahoo/<league>/players/<download-id>/`),
then commits a manifest (`players/manifest.json`) naming that download's
pages and removes the pages of the snapshot it replaces. A download that fails
partway never touches the committed snapshot; its pages are left unreferenced. The import replaces `yahoo_league_players` for the
league: it upserts every player with Yahoo's eligible positions (all of them,
never collapsed to a primary position), status and injury note, then deletes
players the new snapshot no longer lists. A missing or empty snapshot deletes
nothing. Pages or player keys that do not belong to the manifest's league and
game key fail the import.

The pool is downloaded again once it is older than `--yahoo-player-pool-max-age`
hours (default 12) and not at all once the league's season has ended. Stand-in
leagues make no Yahoo calls and have no pool.

Players map to NHL players through `players.yahoo_id`; unmatched players
(typically rookies) stay in the pool and are reported.

## Roster feasibility

`draft.CheckRoster` places players on a league's slots with a maximum
bipartite matching, filling starting slots first, then reserve slots, then the
bench, so a C/LW player moves to whichever slot lets the most players start.
Flex slots accept their base positions (`Util`: C/LW/RW/D, `F`: C/LW/RW,
`W`: LW/RW), the bench accepts any player with a base position, and reserve
slots (`IR`, `IR+`, `NA`) or unknown slots accept only players Yahoo itself
lists as eligible for them — reserve eligibility is never inferred from a
status. Players without eligibility are reported, never dropped.

## Identities

Yahoo league IDs are unique only within one Yahoo game (season). The legacy
`yahoo_*` tables key leagues by the numeric ID alone, so a league ID reused in
a later season would overwrite the earlier season's settings, teams and draft
results. The importer now refuses that with a non-retryable
`YahooLeagueIdentityCollision` error instead of overwriting. The new tables are
keyed by season and full league key (`<game_key>.l.<league_id>`); pool rows
keep both Yahoo's cross-season `player_id` and the game-scoped `player_key`.
Re-keying the legacy tables by season is left for a later migration; the
guard makes a collision visible instead of silently corrupting data.

## Reports

```bash
puckdb draft rules --draft-season 2026 --draft-leagues 1001,1002 --draft-output league-rules-2026.md
puckdb draft pool  --draft-season 2026 --draft-leagues 1001,1002
```

`draft rules` renders the leagues side by side as Markdown (league facts, the
user's team and draft position, draft time, scoring categories with direction
or weight, roster slots, every other setting) followed by per-league warnings:
temporary stand-in rules, stale settings or pool, scoring inputs rankings
would refuse, unmodeled slots or settings, missing draft time or team, and
pool coverage gaps. `draft pool` lists players without eligibility or without
an NHL mapping.

## Not yet verified against Yahoo

Yahoo answers 403 for the 2026 leagues while the application's access request
is pending, so the following follow Yahoo's documented response shapes but
have not been checked against a live 2026 response: the `stat_modifiers` and
`stat_position_types` layout, whether the players collection without a status
filter returns rostered players too, and which slot names Yahoo lists in
`eligible_positions` for injured or not-active players. All fixtures in
`internal/fixtures/yahoofixtures` are synthetic for the same reason.
