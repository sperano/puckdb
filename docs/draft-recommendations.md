# Deterministic draft recommendations

`internal/draftrecommend` evaluates one immutable ranking snapshot against one
version of the reconciled live draft board. The read path is local and
deterministic: it does not call Yahoo, a news provider, or an LLM.

## Frozen input boundary

Each evaluation records the complete input needed to reproduce the result:

- the effective `draftsession.State` and its version;
- whether the session is safe or stale;
- one `draftrank.Snapshot` and scenario;
- the user's team and current roster, including keepers;
- the verified chronological pick slots and their current owners;
- explicit category and risk preferences;
- optional sourced unavailability and ADP observations; and
- the evaluation timestamp.

The chronological order is deliberately an input. Yahoo's normalized league
rules currently identify the draft type but do not contain a verified future
pick order or traded-pick ownership. The service therefore never assumes a
snake draft. A caller may omit the order and still receive player advice, but
the result labels the turn estimate unavailable. When supplied, pre-filled
keeper slots are skipped and each slot's supplied owner determines turns, so
traded picks work without special cases.

## Eligibility and exclusions

Recommendations exclude every player on the effective upstream/manual board,
the supplied roster, the keeper set, and the explicitly unavailable set. A
partial board, unresolved manual conflict, or stale session produces a labeled
result with no candidates.

Ranking filters continue to use base positions (`C`, `LW`, `RW`, `D`, `G`).
Ranking snapshots now also retain `RosterEligiblePositions`, the full Yahoo
eligibility list. Candidate feasibility uses that full list with
`draft.CheckRoster`, so maximum matching can move multi-position players and
can use `IR`, `IR+`, `NA`, flex, and bench slots only when Yahoo eligibility
allows it. An infeasible or unresolved current roster fails closed.

## Value and explanations

Best overall value sorts the selected scenario's league-relative adjusted
value after explicit category preferences and risk tolerance. Best roster fit
first favors additions that create another starting assignment, then active
over reserve/bench placement, scarcer feasible slots, and marginal value.

Every candidate stores the numeric inputs behind its deterministic fallback
explanation:

- league, strategy, risk-adjusted marginal value and news delta;
- assigned slot and additional starting assignments;
- position rank, next feasible position rank, tier and tier drop;
- count of remaining players feasible for the slot;
- strongest and weakest category contributions; and
- news alerts, assumptions, and tradeoffs.

Workload limits, game/start caps, bench policy, replacement value, scoring
format, and head-to-head versus season-long objectives are already applied by
the immutable ranking snapshot. Comparing its baseline and selected scenario
values shows the marginal news cost after those league-specific replacement
assumptions.

ADP never changes player value. A wait-risk label appears only when the input
names the ADP source, version, as-of time, and player value. It describes an
estimate and always states that next-turn availability is uncertain.

## Persistence and replay

Migration `000020_draft_recommendations` adds
`draft_recommendation_runs`. Each row indexes the league, session version,
ranking snapshot/identity/version, projection snapshot/model version, rule
hash, scenario, strategy, timestamp, and observed evaluation latency. It also
stores the complete JSON input and result, including numeric reasons. Keeping
the frozen input makes audit and replay independent of ranking-snapshot
retention.

`draftrecommend.Repository` saves, loads, and finds the latest run for an
exact session version. It verifies source versions before writing and after
reading. `EvaluateReplay` reports whether historical selections matched the
best-value or best-fit recommendation and returns median and 95th-percentile
evaluation latency.

Generated prose may be layered on later, but it must only restate the stored
reasons. The deterministic explanations remain available when no LLM is
configured or reachable.
