# Baseline player projections

`internal/projection` produces versioned, league-neutral NHL projection
snapshots. League scoring is deliberately downstream: one projection snapshot
can be scored differently for each Yahoo league without changing its inputs.

## Model

The `nhl-baseline-v7` model uses completed NHL regular-season games from the
three seasons before the target season. Live, postponed, preseason and playoff
games, and games from the target season or later, are excluded from model
inputs, except that the goalie start share (below) reads the clubs' recent
playoff starts.
Each season is weighted by `season_decay ^ age`, where the immediately prior
season has age zero. Traded-player rows are summed before workload is
calculated, so two club rows in one season still count as one season.

Skater counting rates use weighted time on ice and regress toward a
forward/defense peer rate. Version 2 added faceoffs won and lost, which regress
toward a centre or non-centre peer rate instead, since faceoff usage is far more
position-specific than the other counting stats: lumping centres in with
wingers under the forward/defense split would badly misstate both groups' peer
rate. Expected games and time on ice per game come from weighted historical
workload. A goalie's games are his appearances (games with positive time on
ice): NHL boxscores also list the dressed backup with no time on ice, and
through `nhl-baseline-v5` those bench games counted as games played (see
"Goalie appearances" below). Goalie save and goals-against rates regress by
shots faced; wins and shutouts regress by starts. Goalie GAA and save
percentage are derived from projected goals against, saves, shots, and time on
ice rather than averaging historical ratios. The most recent historical position
is retained as context, and the team is the player's target-season club when
its rosters are imported (otherwise the most recent historical club). TOI and
power-play production represent observed role; the baseline does not guess at
unobserved offseason role changes. Team changes that the target season's rosters
show get the team-environment adjustment below.

### Age curves

Version `delta-v1` learns age steps from completed regular seasons starting in
2005-06 and ending before the target season. Age is measured on January 1 of
the season-ending year. Each player-season is aggregated before the model pairs
consecutive seasons. For every position group (C, W, D, G) and aged count statistic,
the step is the mean change in per-60 rate weighted by the harmonic mean of
the two seasons' time on ice. LW, RW, and the generic F position belong to W.
Seasons without a birth date or positive time on ice do not enter the fit.
Faceoffs remain position-specific projections from `nhl-baseline-v2`, but are
not age-adjusted because `delta-v1` was not fitted or evaluated for them.

The model adds the learned steps from the player's age in their most recent
history season through their target-season age to the baseline counting rate.
An age with no observed pair has a zero step. Games, starts, and time on ice
remain workload forecasts. Skater points come from aged goals plus assists;
goalie save percentage and goals-against average come from the aged components.
The complete ordered steps, pair counts, training window, age convention, and
curve version are stored in the projection config and included in its hash.
The source-data hash also covers the training seasons and birth dates. The
combined `nhl-baseline-v4` retains `nhl-baseline-v3` linemate adjustment and
adds age curves; existing v3 snapshots remain unchanged. `nhl-baseline-v5`
keeps both and adds the team-environment adjustment below; `nhl-baseline-v6`
keeps all of it and counts goalie appearances instead of dressed games.
`nhl-baseline-v1` and
`nhl-baseline-v2` snapshots also retain their original config and source-data
hash formats and can still be loaded.

### Goalie appearances

`game_goalie_stats` has a row for every goalie a boxscore lists, including
the backup who dressed and never entered (zero time on ice). Through
`nhl-baseline-v5`, goalie games played were those rows, so a backup's bench
games counted: in 2025-26 Dobes had 77 rows, 43 appearances and 42 starts;
Montembeault 53/25/23. That inflated projected games, diluted shots and time
on ice per game across games not played, let bench games pick a traded
goalie's context club, and kept seasons with no appearance in the history.

`nhl-baseline-v6` reads `games_appeared` (games with positive time on ice)
from the goalie history and evaluation queries and uses it wherever games
enter the goalie model: projected games, the per-game shot and TOI rates and
their peer rates, history games, the context club and the exclusion of
seasons without games. Its held-out evaluation scores goalie games against
appearances too. Earlier versions keep dressed games, and their config,
source-data and evaluation hashes, projections and evaluation metrics are
pinned unchanged by `internal/projection/published_pin_test.go`. Skater rows
come from the boxscore's dressed lineup (scratches are in `game_scratches`),
so skaters have no equivalent bench rows and are unchanged.

### Goalie start share (v7)

The v6 goalie workload is a decay-weighted per-season average of games and
starts, with no view of who holds the net at season's end. Montreal 2025-26:
starts Dobes 42 / Montembeault 23 / Fowler 17, but Dobes took 15 of the last
23 regular-season starts and all 19 playoff starts, and Montembeault started
once after February; v6 projects Dobes and Montembeault at about 34 starts
each.

`nhl-baseline-v7` reads each club's most recent `goalie_share_window_games`
started games before the cutoff, regular season and playoffs, from
`ListProjectionGoalieRecentStarts` (`starter = TRUE`; a team-game with no
flagged starter contributes no row). A goalie's recent share is
`Σ w·[he started] / Σ w` over those games, where
`w = 0.5^((rank−1)/goalie_share_half_life_games)`, times
`goalie_playoff_weight` for a playoff game. With a playoff weight of 0 the
window counts regular-season games only. When the goalie's target-season
club (the target-season rosters; for a backtest, his first appearance of the
held-out season) is the club of his most recent start, his starts become
`blend·(share × max_games) + (1−blend)·(v6 starts)` with
`blend = goalie_share_blend`, and his v6 relief appearances
(`v6 games − v6 starts`) stay on top. A goalie who changed clubs, has no
recent start or has no target club keeps his v6 starts. Each club's goalies'
starts are then scaled down proportionally if they sum to more than
`max_games`. Shots and TOI follow games; wins and shutouts follow starts. A
zero blend turns the share off. v1 to v6 never load or hash the recent starts,
and their hashes and outputs are pinned by `published_pin_test.go`; the four
parameters are stored in typed snapshot columns (migration 000017).

The defaults (window 60, half-life 20 games, playoff weight 1, blend 0.5) are
**provisional**: they were chosen conservatively and have not been
backtested. TODO: add an integration backtest (build tag `integration`,
`PUCKDB_TEST_PG_URL`, modeled on `aging_integration_test.go`) comparing v6
and v7 goalie games started, games played and wins MAE on target seasons
2023-24, 2024-25 and 2025-26 over a grid of half-life (10, 20, 40), playoff
weight (0, 1, 2) and blend (0.25, 0.5, 0.75) at window 60, run it against the
re-imported production data, and set the defaults from it.

### Historical linemate context

Versions 3 through 6 neutralize historical even-strength linemate context. Shift
boundaries form half-open on-ice segments. A segment counts only when both
teams have the same number of active skaters, from three through five, and
exactly one goalie each; goalies, power plays, penalty kills, empty-net
advantages and line-change 6v6 artifacts are excluded. The shift chart import
precomputes these segments into `even_strength_segments`: after a game's shifts
are upserted, its rows are deleted and rebuilt in one transaction from the
stored shifts and the game's box-score rows, which tell skaters from goalies
(box scores are imported first). The projection query only filters eligible
games and aggregates those rows, so a game imported before the table existed
contributes nothing until its shift chart is imported again. A goal counts for
a scorer or assister when its clock falls in `(start, end]` of one of that
player's segments. Each teammate's even-strength points-per-60 rate is weighted by
shared seconds. Forward and defense contexts use separate exposure-weighted
averages. The model scales the non-power-play share of projected goals and
assists toward average context; points remain their sum, power-play production
is preserved, and no other statistic changes. The correction is
reliability-weighted against four times the skater TOI prior, so sparse shift
coverage stays close to neutral. This is strictly a correction for past
context and makes no assumption about the target season's lines. In versions 4
and 5, this correction is applied before the delta-method age adjustment; points are
recomputed from the adjusted goals and assists.

The default parameters are:

| Parameter | Value |
| --- | ---: |
| Lookback | 3 seasons |
| Season decay | 0.40 |
| Skater regression prior | 9,000 seconds of TOI |
| Linemate regression strength | 0.25 |
| Goalie regression prior | 500 shots faced |
| Goalie shutout minimum TOI | 3,540 seconds |
| Maximum games | 82 |
| Interval z-score | 1.28 |
| Uncertainty bounds | 10%–100% |
| Insufficient-history threshold | 10 games |
| Team-environment prior | 328 games (four seasons) |
| Team-environment maximum change | ±5% |

The decay and skater prior were selected from a small grid of 15 combinations
(`decay` 0.40–1.00, prior 9,000–36,000 seconds). Across the nine skater
scoring statistics and three held-out seasons below, the selected combination
had a mean relative MAE of 0.935 against the previous-season baseline. The
grid and sample are small, so these defaults are versioned parameters rather
than claims of a final calibrated model.

## Team environment

`nhl-baseline-v5` adds a team-environment adjustment for skaters who change
clubs (`internal/projection/teamenv.go`). It is applied last, after the
linemate correction and the age curve.

- **Club environment.** For each club and season, `ListProjectionTeamSeasons`
  reads from `games` the goals it scored (the shootout winner's extra goal
  dropped) and its shots on goal, and from `play_events` the power-play
  opportunities it drew: minor and bench-minor penalties charged to the
  opponent, counted only in games that have play-by-play. Coincidental minors
  over-count slightly. Seasons are weighted with the same decay and lookback
  as player history, and each per-game rate is regressed toward the league
  average by 328 games of league-average play, then expressed as an index
  (1 = league average).
- **Target club.** `ListProjectionTargetTeams` reads each player's club from
  the target season's imported rosters (the most recently updated row wins).
  Before those rosters exist, nobody is adjusted.
- **Adjustment.** A skater whose weighted history includes games for clubs
  other than the target club has goals and assists scaled by the target club's
  goals index over their history's games-weighted goals index. A season split
  by a trade is credited to the clubs its games were played for
  (`ListProjectionSkaterClubGames`), not only to the last club. Shots on goal
  use the shots index, and power-play points the power-play-opportunity index.
  Points follow goals and assists. Every factor is capped at ±5%. Clubs with no
  environment rows count as league average. Plus/minus, penalty minutes, hits,
  blocks, faceoffs, workload and every goalie stat are unchanged.
  A skater with no time on ice has no rate projections to scale and gets no
  adjustment.
- **Explanations.** The applied factors are stored per player
  (`projection_players.team_environment`, migration `000014`). The ranking
  explanations then show a line such as `team environment: NYR → LAK (100% of
  weighted history with other clubs); goals ×1.031, assists ×1.031, shots
  ×0.994, power-play points ×1.012; club rates regressed toward league
  average`. Snapshot assumptions state how many skaters changed clubs, or that
  none did because the target season's rosters are not imported.

Snapshots of earlier versions have zero team-environment parameters, which
means "off"; config validation rejects non-zero ones for them. Their config and
source-data hashes are unchanged, so they still load. Club environments and
split-season club games are read over the three-season lookback only, not the
age curve's longer training window.

### Team-change backtest

`projection.EvaluateTeamChanges` scores the team-dependent stats of *movers*:
skaters whose target-season club is not their most recent history club. It
compares the adjusted model with the same config minus the adjustment, and
with previous-season totals, on the same players. The backtest in
`internal/projection/teamenv_backtest_live_test.go` reruns it on the public NHL
stats API's regular-season aggregates (no production data needed):

```sh
go test -tags=livenhl -run TestTeamEnvironmentBacktest -v ./internal/projection/
```

That test has a narrower mover definition. A mover has exactly one club in
the season before the target season and exactly one, different, club in the
target season. History seasons split across clubs count as a league-average
environment, and the API's official power-play opportunities stand in for the
penalty-event count. Those aggregates carry no birth dates or shift data, so
the v5 age curve has no steps and the linemate correction stays neutral: the
backtest isolates the team-environment adjustment. Results with the defaults,
run on 2026-09-27 and unchanged under v5 (`adjusted MAE / unadjusted MAE /
previous-season MAE`):

| Statistic | 2024-25 movers (n=144) | 2025-26 movers (n=121) |
| --- | ---: | ---: |
| Goals | 4.08 / 4.06 / 4.26 | 3.82 / 3.84 / 4.01 |
| Assists | 6.56 / 6.55 / 6.90 | 6.29 / 6.27 / 6.49 |
| Points | 9.47 / 9.44 / 9.81 | 9.25 / 9.18 / 9.62 |
| Shots on goal | 32.87 / 32.77 / 33.81 | 29.66 / 29.83 / 28.18 |
| Power-play points | 3.05 / 3.05 / 2.83 | 2.54 / 2.55 / 2.57 |

The adjustment does not measurably improve season totals. Every cell is
within 0.8% of the unadjusted model, and the sign flips between the two
seasons. Totals are dominated by games-played error, so the test also scores
the movers' per-60 rates, weighted by actual TOI (`adjusted / unadjusted`
MAE). Goals 1.009 and 0.991; assists 0.992 and 1.001; shots 1.001 and 0.984;
power-play points 0.995 and 0.995. Only power-play points improve in both
seasons.

Over a grid of priors (0–328 games) and caps (5–25%), the mean ratio of
adjusted to unadjusted season-total MAE was between 1.001 and 1.011 every
time. Lighter regression and larger caps were worse, and none was better than
no adjustment. The defaults are therefore the most conservative corner of the
grid: heavy regression and a ±5% cap. The adjustment keeps offseason moves
visible and explained in the rankings but barely moves values. Treat it as a
presentation of known team changes, not as a proven accuracy gain. Goalies
are not adjusted: team defense affects shots against and wins, but this
backtest does not cover it.

## Historical evaluation

### Delta-v1 age-curve backtest

The `delta-v1` age curve was tested by projecting 2025-26 from the three
preceding seasons. Curve fitting used 2005-06 through 2024-25 only; the
2025-26 season was held out for outcomes. The run used NHL public
regular-season aggregate reports retrieved on 2026-09-26, which do not contain
the shift-derived linemate inputs. The no-aging skater MAEs closely track the
earlier production-derived results below, and several match at the displayed
precision; small differences reflect the distinct aggregate source and
retrieval date. Both variants were scored on exactly the same players. Delta
is aged MAE minus no-aging MAE, so a negative value is an improvement.

| Skater statistic | n | No aging MAE | Aged MAE | Delta |
| --- | ---: | ---: | ---: | ---: |
| Games played | 791 | 16.041 | 16.041 | 0.000 |
| TOI seconds | 791 | 16,680.983 | 16,680.983 | 0.000 |
| Goals | 791 | 4.234 | 4.162 | -0.072 |
| Assists | 791 | 6.412 | 6.297 | -0.115 |
| Points | 791 | 9.625 | 9.374 | -0.251 |
| Plus/minus | 791 | 9.191 | 9.096 | -0.095 |
| Penalty minutes | 791 | 12.044 | 12.086 | +0.042 |
| Power-play points | 791 | 2.772 | 2.662 | -0.110 |
| Shots on goal | 791 | 29.843 | 28.953 | -0.890 |
| Hits | 791 | 26.229 | 26.708 | +0.480 |
| Blocked shots | 791 | 15.804 | 16.182 | +0.379 |

| Goalie statistic | n | No aging MAE | Aged MAE | Delta |
| --- | ---: | ---: | ---: | ---: |
| Games played | 83 | 10.738 | 10.738 | 0.000 |
| Games started | 83 | 10.572 | 10.572 | 0.000 |
| TOI seconds | 83 | 37,481.273 | 37,481.273 | 0.000 |
| Wins | 83 | 6.325 | 6.299 | -0.026 |
| Shutouts | 83 | 1.234 | 1.194 | -0.039 |
| Shots against | 83 | 304.874 | 304.165 | -0.709 |
| Saves | 83 | 278.681 | 276.980 | -1.701 |
| Goals against | 83 | 29.434 | 29.692 | +0.258 |
| Save percentage | 83 | 0.0188 | 0.0178 | -0.0010 |
| Goals-against average | 83 | 0.4653 | 0.4653 | 0.0000 |

The curve improves six of the nine aged skater categories; penalty minutes,
hits, and blocked shots regress. Goalie wins, shutouts, shots against, saves,
and save percentage improve, while goals-against MAE worsens. Workload MAEs
are identical because games, starts, and TOI are intentionally not aged.
The same comparison can be rerun through the production SQL loaders against a
populated test database. That test compares `nhl-baseline-v4` with the
published linemate-only `nhl-baseline-v3`, isolating the age-curve effect while
retaining identical historical linemate context:

```bash
PUCKDB_TEST_PG_URL='postgres://...' \
  go test -tags=integration ./internal/projection -run TestAgingBacktest20252026 -v
```

### Baseline-v1 evaluation

The table below evaluates `nhl-baseline-v1` using production game-level
regular-season aggregates available on 2026-09-20. For each target season, the
model received only earlier seasons; the target season supplied outcomes. Both
models were scored on players with a target-season row and a previous-season
row. Each cell is `model MAE / previous season MAE`. Stored evaluations include
the configuration hash, source-data hash, projection cutoff, and observation
time.

This table records the version 1 external evaluation. Its production-derived
source rows are not committed, so the exact numbers cannot be reproduced from
this repository alone. Given equivalent season aggregates,
`projection.Evaluate` reproduces the calculation and rejects overrides so
external projections cannot leak into held-out results.

| Statistic | 2023-24 (n=782) | 2024-25 (n=778) | 2025-26 (n=791) |
| --- | ---: | ---: | ---: |
| Games played | 16.01 / 16.64 | 15.78 / 16.03 | 16.04 / 16.16 |
| TOI seconds | 16,863 / 17,745 | 16,497 / 17,004 | 16,675 / 16,689 |
| Goals | 4.43 / 5.02 | 4.21 / 4.51 | 4.23 / 4.41 |
| Assists | 6.43 / 7.21 | 6.51 / 6.83 | 6.41 / 6.66 |
| Points | 9.80 / 10.80 | 9.50 / 9.99 | 9.62 / 9.75 |
| Plus/minus | 9.39 / 11.15 | 9.68 / 11.14 | 9.19 / 10.42 |
| Penalty minutes | 12.67 / 13.76 | 11.75 / 13.41 | 12.02 / 12.95 |
| Power-play points | 3.08 / 3.38 | 3.08 / 3.25 | 2.78 / 2.89 |
| Shots on goal | 31.21 / 33.80 | 29.66 / 31.33 | 29.85 / 29.13 |
| Hits | 27.96 / 29.02 | 28.77 / 29.53 | 26.22 / 25.80 |
| Blocked shots | 18.70 / 19.44 | 17.39 / 18.72 | 15.79 / 16.88 |

The model improves most skater MAEs, but the 2025-26 previous-season baseline
is better for shots and hits. This is a baseline for later ranking and
sensitivity work, not evidence that every category has improved. Faceoffs won
and lost were added in `nhl-baseline-v2`, and the team-environment adjustment in
`nhl-baseline-v5`, after this evaluation was recorded; neither is reflected in
the table above.

### Version 3 linemate holdout

`projection.EvaluateLinemateAdjustment` compares version 3 against the same
configuration with only `LinemateRegressionStrength` set to zero. It uses the
same player/stat sample on both sides and returns MAE and RMSE per skater stat.
For a 2025-26 holdout, `Repository.LoadEvaluationInput` reads 2022-23 through
2024-25 as model history and 2025-26 only as outcomes.

The numeric 2025-26 result is not recorded here. This repository contains no
production-derived shift or game aggregate export, and database-backed tests
skip when `PUCKDB_TEST_PG_URL` is unset. The holdout must therefore be run
against an approved 2022-26 database before the coefficient can be considered
empirically validated. No error change is inferred from synthetic fixtures.

Goalie component and ratio behavior is covered by deterministic tests. A live
goalie backtest was not recorded in this change because approval to export the
production-derived goalie aggregate was denied. It should be run before goalie
rankings are used for the draft helper.

## Snapshots and coverage

Migrations `000004` and `000012` store immutable snapshot identity, a
source-data hash, model parameters, and linemate context in typed columns;
`000013` adds the persisted v4 aging curve; `000014` adds the v5
team-environment parameters and per-player adjustments, and requires the aging
curve for v5 as well; `000016` requires it for v6. Player metadata is separate from
normalized stat rows, where each value stores its mean and interval. Exact
rebuilds replace their player rows atomically, which makes retrying a workflow
safe; a historical backfill produces a distinct source hash and snapshot.

Internal projections use `nhl:<player_id>` keys. Imported or manual overrides
can add rookies and unresolved players that have no NHL history. These rows
record their source and provider rather than silently fabricating history.
Internal rows with fewer than ten historical games remain present and carry an
`insufficient_history` flag. Callers pass the current draftable pool in
`Input.PlayerPool`; pool players without usable NHL history remain in the
snapshot with empty values and a complete `missing_stats` list. Historical
skaters whose game rows have no TOI are handled the same way instead of being
discarded.

`ValidateCoverage` maps enabled Yahoo stat IDs to the versioned projection
statistics and returns a deterministic `CoverageError` for every unsupported
category or missing player value. Display-only categories are ignored only
when the caller marks them as such. The currently supported Yahoo scoring IDs
are 1–5, 8, 14, 16, 17, 19, 22–27, 31 and 32; other IDs fail explicitly. Complete live
coverage requires both the verified league categories and the complete player
pool from the Yahoo import. `Repository.BuildSnapshot` accepts both in its
`BuildRequest` and refuses to persist a selected snapshot that fails coverage.

Yahoo approval is still required to validate that every player in the live
2026 draftable pool has either an internal projection or an explicit
imported/manual row. Missing Yahoo eligibility cannot be inferred from NHL
history.

When overrides are supplied, `BuildSnapshot` first persists the unchanged
internal baseline, then stores the override snapshot under its own source-data
hash. This keeps imported or later news-adjusted values separate from the
baseline they replace.
