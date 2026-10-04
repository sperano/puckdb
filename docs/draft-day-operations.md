# Draft day operations

This runbook covers the Yahoo 2026 leagues 5621 and 29249. It is an offline
operating procedure; a completed live rehearsal must be recorded separately
for each full Yahoo league key. Never infer a draft date, user team, league
settings, pool completeness, or OAuth readiness from a synthetic fixture.
The helper records and explains decisions. It does not submit picks to Yahoo.

## Readiness gate

Before each league's scheduled draft, obtain the date and time from that
league's imported Yahoo metadata and record the full league key, season,
account/team name, and time zone. Stop that league's preflight if any value is
missing or differs from the Yahoo page.

Run these checks against the configured database and Yahoo account:

```text
puckdb yahoo check-access --yahoo-check-season 2026
puckdb sync fetch-seasons import-seasons --season 2026
puckdb sync draft --league <full-league-key>
puckdb draft rules --draft-season 2026 --draft-leagues 5621,29249 --draft-output /secure/path/draft-rules-2026.md
puckdb draft pool --draft-season 2026 --draft-leagues 5621,29249
puckdb sync refresh-news --season 2026 --news-force
puckdb news report --news-season 2026 --news-output /secure/path/news-coverage-2026.md
puckdb sync refresh-draft-rankings --season 2026 --draft-leagues 5621,29249
puckdb draft session status --league <full-league-key> --draft-season 2026
```

The access check must report `AUTHORIZED`. For each league, confirm the
imported rules report has the expected team, scoring, roster, draft format,
pick clock, draft date, draft position, and the owner's team. The pool report
must have no unexplained missing eligibility or NHL mappings. Confirm that
the rankings snapshot covers the imported pool and is `ready`, and that the
latest news coverage report lists the sources that succeeded and any stale,
failed, or missing sources. Record the rules hash/version, ranking snapshot ID,
projection version, news as-of time, and session state/sync versions in the
rehearsal notes.

Today, Yahoo access for these 2026 leagues has not been verified from this
repository, and the committed Yahoo XML fixtures are synthetic. A passing
offline test does not satisfy this gate. Do not call a league ready until a
real authenticated refresh and rule/pool comparison succeed.

## Refresh and rehearsal

Refresh settings and the draft session first. Refresh projections and rankings
after the pool and settings have been checked, then refresh news and rankings
again if news changed. The draft ranking workflow records immutable snapshots;
capture the chosen snapshot ID so an export and a later audit refer to the same
data.

Export the complete player list before the draft and keep a copy with the
rehearsal artifacts:

```text
puckdb draft rankings --draft-league <full-league-key> --draft-season 2026 --draft-format csv --draft-output /secure/path/<league-id>-rankings.csv
puckdb draft rankings --draft-league <full-league-key> --draft-season 2026 --draft-format json --draft-output /secure/path/<league-id>-rankings.json
```

The saved CSV/JSON is a ranking export, not a database backup. Protect it as
draft data. Back up PostgreSQL using the site's normal database backup
procedure before the draft; verify that the backup is restorable under that
procedure. Do not treat a local export as a replacement for the database
backup, which also contains session history and audit records.

The first sync above refreshes and imports Yahoo settings as part of the 2026
season workflow even when NHL standings are future-dated. The forced news
refresh deliberately precedes the ranking refresh so the selected snapshot's
news as-of time can include those reports. Repeat the news report and ranking
refresh immediately before the draft, and retain both timestamped outputs.

During rehearsal, replay a complete draft in a disposable database or against
isolated test repositories. Exercise each position filter, recommendations,
shortlist, manual add/correct/undo, conflict resolution, and export. Interrupt
the watch after persisted picks exist, restart it, and verify the same picks,
roster, availability, and recommendations are recovered. Replay identical
polls and duplicate/partial feed rows; identical input must not duplicate picks,
partial input must not delete unseen picks, and unsafe input must suppress
recommendations. Use a timestamped uncertain suspension followed by a later
reinstatement; the earlier as-of view must retain the uncertainty and the
later view must apply the return only after its recorded time. Never include
future events when evaluating a historical recommendation.

The repository currently contains no target-league complete-draft replay or
verified full-pool fixture. Record this as an unmet rehearsal item until real
league metadata and a complete replay artifact are available; do not substitute
the small synthetic ranking fixture and label it end-to-end league validation.

## Polling, freshness, and cost limits

The named `sync draft` defaults are a 30-second interval after successful
polls, a 300-second maximum backoff for failures, and a 20-second final
reconciliation timeout on cancellation. The two-second browser poll reads the
local board and does not make a Yahoo request. Keep these defaults for the
first live rehearsal. Change them only after collecting per-league capability
observations with `puckdb draft session capability --league
<full-league-key> --draft-season 2026` and checking Yahoo rate-limit responses.

The capability report measures poll duration, success/failure class, partial
payloads, changed snapshots, and the largest poll-to-change detection gap.
Yahoo draft results do not provide per-pick publication timestamps, so the gap
is only a polling upper bound. Recommendation runs record their evaluation
latency. Capture P50/P95 from replay and stored runs for the actual pool before
claiming a latency target; the small unit fixtures are not representative.

News extraction defaults cap a refresh at 100 model calls and 500,000 combined
input/output tokens, with at most 3 attempts per version. The token cap can
overshoot by up to concurrency times one call because usage is counted after
responses. PuckDB has no provider price table, so these are token/call limits,
not a dollar estimate. Keep extraction disabled unless the selected provider,
model, keys, token cap, and budget owner are known. News source freshness and
extraction failures are visible through `puckdb news report` and the worker
refresh result; do not treat an extraction failure as fresh validated news.

Watch the worker logs for poll failures, authentication errors, HTTP 429s,
partial snapshots, and skipped malformed rows. The capability report is the
operational summary; the `/metrics` endpoint and existing service logs are the
runtime health views. An unsafe or stale board is an alert to switch to manual
tracking, not a reason to assume the last poll was complete.

## During the draft and recovery

Start the local watch and browser board for the exact full league key:

```text
puckdb sync draft --league <full-league-key> --watch
```

Keep the board's freshness/safety status visible. Position filters do not
renumber baseline ranks. Treat current turn estimates as estimates unless a
verified chronological order was loaded. No button or command in this helper
submits a real Yahoo pick.

If Yahoo or OAuth fails, note the last successful poll and continue with the
local board. Manually add or correct picks only after confirming the round,
slot, team key, and player key in Yahoo. Undo creates a persisted tombstone; a
later Yahoo disagreement remains a conflict until explicitly resolved with
`keep-manual` or `accept-upstream`. Refresh once connectivity returns and
inspect `draft session status` before trusting recommendations. A restart is
safe because the board and manual history are persisted; start the watcher
again and verify the session state/sync versions advance as expected.

For last-minute news, run a news refresh, inspect `news report` and `news
events`, and refresh the league rankings after any accepted event or override.
Check the evidence/report time and resulting scenario before returning to the
board. An article posted after the stored snapshot's as-of time cannot be
claimed as part of that snapshot.

## Rehearsal record and known limits

For each league, retain the actual draft date/time and team identity; access
check output; rules and pool reports; source freshness; settings/ranking/news
versions; complete replay and export; restart/duplicate-feed results; observed
runtime and token counts; and any manual fallback or incident notes. Record
the command, timestamp, and outcome rather than checking a box without
evidence.

Until the live Yahoo checks and full-pool replays exist, remaining limits are:

- target league access, rules, draft dates, user teams, and position
  eligibility have not been verified against authenticated live responses;
- no complete draft replay exists for either target league, and current
  synthetic player fixtures are intentionally small;
- Yahoo publication latency cannot be measured from draftresults;
- provider dollar cost cannot be computed by PuckDB; and
- no successful target-league rehearsal should be claimed from this runbook
  alone.
