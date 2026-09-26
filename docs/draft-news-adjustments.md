# Draft helper: news adjustments and return scenarios

`internal/newsadjust` turns validated news events into adjustments of a
baseline projection snapshot, so each league's ranking can be recomputed from
adjusted inputs and compared with the baseline. It never changes the baseline,
never reads live state, and never invents a return date: an absence whose
length the sources do not give becomes a range of labeled assumptions, not a
number.

## Inputs

`newsadjust.Apply` reads one `Request`:

| Field | Meaning |
|---|---|
| `Baseline` | A stored projection snapshot (`internal/projection`) |
| `Events` | Validated event versions (below) |
| `Overrides` | Manager overrides, including reset and expired ones |
| `Policy` | Every assumption that turns an event into numbers |
| `Season` | Season start, end and games per team |
| `AsOf` | The time the adjustment describes |
| `LeagueKey` | Selects league-scoped overrides |
| `CoverageWarnings` | News coverage gaps at `AsOf`, carried into alerts (`CoverageWarnings` builds them from `news.EvaluateCoverage`) |

### Events

`Event` is the contract with validated event extraction
(`internal/newsevent`, see `docs/draft-news-events.md`); `LoadExtractedEvents`
builds it from the stored events (below). Each event has a stable ID and
increasing versions, a player key (the projection's key), a type
(`suspension`, `injury`, `absence`, `return`, `trade`, `role_change`), a
status (`confirmed`, `reported` or `rumor`), a lifecycle (`active`,
`superseded`, `retracted`, `resolved`), report, record and effective times,
an attributed duration, reported role changes, the event IDs it supersedes,
at least one evidence reference to a stored article version, and an optional
hold reason. A `reported` event (attributed to a reporter, not announced)
counts like a confirmed one, but a confirmed report is preferred as an
incident's primary report. A held event only alerts: it never changes a
projection or closes another event, and only its identity is validated.

A duration carries a number only when the sources give one: `games` (with a
count) and `until` (with a return time). `day_to_day`, `week_to_week`,
`month_to_month`, `indefinite` and `unknown` carry none; `season` means out
for the season. `Validate` rejects a count on any other kind, a missing
return time, missing evidence and impossible chronology. A resolved event may
end before its start: the return was reported before the absence began, so it
removes nothing.

### Events from extraction

`Repository.LoadEvents` (`LoadExtractedEvents` over any query set) reads every
stored extraction event about the baseline's players (by NHL ID, or by the
Yahoo ID of a `<game>.p.<id>` pool key), with its evidence rows and lifecycle
transitions, and `ConvertExtracted` turns each into versions:

- **Versions.** Rows are grouped by the report (extraction and article
  version) that recorded them. Each report that changes what the adjustment
  sees (new supporting evidence, a lifecycle transition) is a new version,
  recorded when the last of its rows was. A replay at a historical as-of time
  therefore sees what extraction knew then, even from the live tables. The
  event ID is `news-event:<id>`; evidence is one reference per supporting
  article version, with its first quote.
- **Types.** Injuries and suspensions map directly. A reinstatement is a
  return that names the events it resolved in the same report. A change of
  team is a trade when the destination matches exactly one NHL team by
  abbreviation, full or common name. A change of league is an absence when it
  names another league (AHL, ECHL, KHL, Europe…), and a return ending the
  player's earlier assignments when it names the NHL. A stated role maps to a
  goalie role (starter, tandem, backup), a power-play direction (top or first
  unit up; second unit, removed or dropped down) or an ice-time direction
  (top line or pair up; fourth line, bottom six or third pair down) only
  through fixed phrase lists.
- **Durations.** Lengths in days and end dates become an end time; the other
  kinds map one to one.
- **Resolution.** An injury or suspension extraction resolved (by a later
  report, or by the report that states it) ends at that player's
  reinstatement in the resolving report: its stated date, else when it was
  reported.
- **Holds.** An event is held (alert only) when extraction routed it to
  review, when its extractor's latest evaluation on the built-in corpus did
  not pass every release threshold (`newsevent.AutomaticEffectsAllowed`, read
  at load time), when it has no numeric mapping (a roster status, a role or
  league outside the phrase lists, an unknown trade destination), when a
  stated end precedes the start, or when a reinstatement or recall ends
  nothing on record. A held reinstatement does not lift the penalty it
  resolved either.

The review flag and the release gate are read as they stand at load time, not
per version; a stored run keeps the versions it used, so its replay does not
depend on them. Versions that cannot form a valid event, and events without
supporting evidence, are returned as warnings.

## Which events count

Only versions recorded by `AsOf` are visible, and only the latest visible
version of each event. A replay at a historical time therefore sees what was
known then, and loading newer versions does not change it. Every visible
version gets a `Decision` (applied, merged, alert or skipped, with the
reason), kept with the run.

- **Retracted** and **superseded** versions do nothing. A confirmed event that
  lists earlier events in `Supersedes` (a correction) supersedes them.
- **Returns** (reinstatement, activation) close the availability events they
  name. A return that names none closes every earlier open-ended absence of
  the player: one with no game count and no end time, excluding
  previous-season injuries and absences (a previous-season suspension stays
  open). An unrelated later return therefore never stretches a served
  suspension or last season's injury. A return before the season removes the penalty; a
  return during it fixes the absence's length, whatever the scenario. A
  resolved event ends at its own end time.
- **Rumors** stay alerts under the default policy (`rumors: alert`). The
  `conservative` rumor policy applies a rumored absence in the conservative
  scenario only and says so. A rumored return never closes anything, and a
  rumored role change stays an alert under either policy.
- **Incorporated news**: when a projection row's `IncorporatesNewsThrough` is
  at or after the event's report time (an imported projection that already
  accounts for it), the event is skipped. When a later return closes such an
  event, the player gets an alert: the provider's penalty stays until that
  projection is refreshed, because its size is unknown.
- **Repeated reports** of one incident (same player and news incident) merge
  into one claim, so any number of reports applies the effect once. The
  primary report is the confirmed, most authoritative (official, then
  structured, then reporting), newest one.
- **Conflicting reports** of one incident (different timing or duration) set
  the scenarios apart: conservative takes the longest absence, optimistic the
  shortest, base follows the primary report. The player gets an alert.
- **Overlapping absences** (an injury during a suspension) are unioned on the
  schedule, never added.
- **Role reports** resolve per field (team, ice time, power play, goalie
  role) into consecutive segments: a report decides the field from its
  effective start until the next report of that field starts. A reversed
  role keeps its earlier period, and a repeated report never stacks on the
  one before it. At the same start, the newer report wins.

## From events to numbers

Dates map to team games as if games were evenly spread between the season's
start and end. An attributed game count starts at the later of its effective
date and the season start; games served before the season are not deducted
until a report says so. Both assumptions are listed with each affected player.

News from more than the policy's `OffseasonDays` (default 90) before the
opener (about the start of July) is treated as the previous season's. A season-ending report from then ends
with that season. An injury or absence of unknown length from then, with no
newer report, counts in the conservative scenario only; a newer structured
status (Yahoo) or report is a new event and applies normally. Suspensions
carry over, since they are served in games.

Per scenario, the player's effect is:

- **Missed games**: the union of absences; availability is
  `(season games − missed) / season games`.
- **Ice time** and **power play** multipliers for a reported direction,
  weighted by the share of the season each role segment covers and summed
  across segments.
- **Goalie starts**: a reported role (starter, tandem, backup) targets a share
  of the team's games.
- **Team**: a trade sets the new team. Per-game rates stay as projected; team
  context beyond the reported role is not modeled, and the player's
  assumptions say so.

Skater counting stats scale with games and ice time, because the baseline
projects them as per-minute rates. Power-play points scale with the
power-play factor, points gain or lose the same amount, and goals and assists
follow the new point total. Every goalie counting stat (games, starts, ice
time, shots, saves, goals against, wins, shutouts) scales with workload, so
save percentage and goals-against average, their ratios, are unchanged: a
suspension removes starts, not quality. League scoring decides what that is
worth: in a wins/saves points league the goalie's score falls with the
starts, while in a GAA/SV% category league only the goalie's weight in the
team ratio moves.

Each scenario snapshot keeps its own mean and widens every interval to cover
all three scenarios. The player's uncertainty becomes
`hypot(baseline, spread)` (capped at the projection maximum), where spread is
half the optimistic-to-conservative workload range relative to base.

### Policy

`DefaultPolicy` values are **uncalibrated**: PuckDB has no labeled data
linking news to later games, ice time or starts. Every adjustment that uses
one lists it with the policy's `Calibration` label. A policy is validated
(present, finite, conservative never better than base, base never better
than optimistic) and hashed into the adjustment identity.

| Assumption | Conservative | Base | Optimistic |
|---|---:|---:|---:|
| Day-to-day, missed games | 3 | 1 | 0 |
| Week-to-week | 12 | 6 | 2 |
| Month-to-month | 30 | 15 | 8 |
| Indefinite | 41 | 15 | 0 |
| Unknown | 20 | 5 | 0 |
| More ice time, per game | ×1.00 | ×1.08 | ×1.15 |
| Less ice time | ×0.85 | ×0.92 | ×1.00 |
| Power-play promotion | ×1.00 | ×1.40 | ×1.80 |
| Power-play demotion | ×0.40 | ×0.65 | ×1.00 |
| Starter, share of team games started | 55% | 63% | 72% |
| Tandem | 38% | 46% | 54% |
| Backup | 18% | 25% | 32% |

The policy also sets `OffseasonDays` (default 90), the window that separates
the previous season's news from offseason news about the target season.

An indefinite suspension (the Hellebuyck-style fixture) is therefore shown as
a 0–41 game range with 15 in the base scenario, labeled as an assumption, not
as a forecast. A manager who knows better sets an override.

## Overrides

An `Override` has a reason, an optional league scope, an optional scenario,
a creation time, an optional expiry, and a reset time with its reason. Rows
are never deleted: a reset or expiry ends an override, and a replay before
that time still sees it. Kinds:

| Kind | Effect |
|---|---|
| `missed_games` | Replaces the player's total missed games |
| `input` `games_played` | A skater's expected games |
| `input` `games_started` | A goalie's expected starts |
| `input` `toi_per_game` | A skater's ice time per game, in seconds |
| `input` `power_play_factor` | Multiplies a skater's power-play production |
| `exclude_event` | Keeps one event from changing anything |

When two active overrides target the same thing, the league-scoped one wins,
then the scenario-specific one, then the newest. The others are listed as
shadowed. Each applied override records the value it replaced in each
scenario. An input that does not fit the player (starts for a skater) is
ignored with an alert.

## Results and comparison

`Result` holds an adjusted snapshot per scenario (each with its own source
hash), a `PlayerAdjustment` for every player news or overrides touched
(reasons with evidence and evidence age at `AsOf`, per-scenario effects,
changed stats, applied overrides, assumptions, alerts, baseline and adjusted
uncertainty), every decision, the visible event versions, the active and
shadowed overrides, and alerts. `Result.ID` hashes the method version,
policy, baseline identity, as-of time (kept to the microsecond, PostgreSQL's
precision), league, season, visible events, active overrides and coverage
warnings.

`newsadjust.Compare` ranks the baseline and each scenario snapshot with
`draft.BuildRanking` under the same rules, pool and options, and returns one
row per pool player with baseline and scenario rank, tier, score and value,
plus the adjustment behind any difference. It names the league rule
snapshot (`RulesHash`) and the adjustment (`AdjustmentID`) it ranked, so a
comparison can be rebuilt from stored versions. It refuses an adjustment built
from another baseline or holding another league's overrides.

## Storage (migration `000009_news_adjustments`)

| Table | Holds |
|---|---|
| `news_adjustment_overrides` | Every override with reason, scope, expiry and reset |
| `news_adjustment_runs` | One adjustment: identity, policy, baseline snapshot, as-of, league, season, the overrides as they stood at as-of, coverage warnings |
| `news_adjustment_events` | Each visible event version (full payload) with its decision |
| `news_adjustment_scenarios` | The stored projection snapshot of each scenario |
| `news_adjustment_players` | Each player's explanation |

`Repository.SaveRun` stores the scenario snapshots and the run in one
transaction; saving the same adjustment again replaces its rows.
`Repository.Replay` loads the stored baseline snapshot and the stored inputs,
applies them again, and fails unless the identity and every scenario
snapshot match. A later reset of an override does not change what a stored
run replays to.

## Not done here

- **Calibration.** No default is calibrated. A calibrated policy needs a
  labeled set of news events with the games, ice time and starts that
  followed.
- **Real schedules.** Dates map to games by even spacing, not by each team's
  schedule.
- **Team context.** A trade changes the team, not the player's rates.
- **Role phrases.** Roles, leagues and roster statuses outside the fixed
  phrase lists only alert; widening the lists needs labeled examples.
- **Exposure.** No CLI, GraphQL or workflow runs adjustments yet; that is the
  rankings API step.
