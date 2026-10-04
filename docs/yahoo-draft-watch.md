# Yahoo draft watch and manual fallback

The live draft helper uses a dedicated session keyed by Yahoo's full league
key (`<game-key>.l.<league-id>`). It does not submit picks to Yahoo. The legacy
`yahoo_draft_results` table remains the season-sync import; live state is stored
in `draft_sessions` so numeric league IDs reused by another season cannot
collide.

## One-shot refresh and watch

Run migrations first, import the league's settings/rules, and authenticate with
Yahoo. A numeric ID is resolved from the newest matching imported rule snapshot;
use `--draft-season` when the same numeric ID exists in more than one season.

```text
puckdb sync draft --league 500.l.5621
puckdb sync draft --league 5621 --draft-season 2026 --watch
```

Each poll force-refreshes the mutable league status and `draftresults`
resources. A valid response replaces the filesystem file and its parsed Redis
entry before the database transaction runs. Immutable game-key discovery and
unrelated season resources continue to use their normal cache.

`--draft-poll-interval`, `--draft-max-backoff`, and `--draft-final-timeout` are
seconds. The initial 30-second interval is an operational starting point, not a
measured Yahoo promise. Failures back off exponentially to the configured cap.
PostgreSQL advisory locking rejects another one-shot or watched synchronization
for the same full league key. Cancellation performs one bounded final refresh
before releasing the lock.

## Reconciliation contract

A snapshot can replace the upstream board only when Yahoo supplied `count`, the
count equals the raw row count, every slot and Yahoo key is valid, and the
response has authority evidence. Positive incomplete snapshots may add or
correct valid rows, but cannot delete omitted picks. An empty snapshot can reset
the board only when the status is explicitly `predraft` and Yahoo explicitly
sent `count="0"`; an empty drafting/error response preserves prior picks.

Identical snapshots create poll observations but do not advance the state
version or create a board-change event. Corrections, undos, and authoritative
resets are one row-locked transaction. Malformed/unmapped rows and partial or
failed polls make the board unsafe for recommendations while preserving the
last usable picks.

## Manual fallback and conflicts

Manual operations remain available while Yahoo or OAuth is unavailable:

```text
puckdb draft session add --league 500.l.5621 --round 1 --pick 1 \
  --team-key 500.l.5621.t.3 --player-key 500.p.1234
puckdb draft session correct --league 500.l.5621 --round 1 --pick 1 \
  --team-key 500.l.5621.t.3 --player-key 500.p.5678
puckdb draft session undo --league 500.l.5621 --round 1 --pick 1
puckdb draft session resolve --league 500.l.5621 --round 1 --pick 1 \
  --resolution accept-upstream
```

Manual entries are displayed as `source=manual`. Undo is a tombstone, so a
restart does not accidentally restore the locally removed pick. If a later
complete Yahoo board differs from the manual intent, the entry becomes a visible
conflict and recommendations remain suppressed. Resolution must explicitly be
`keep-manual` or `accept-upstream`.

Use `puckdb draft session status --league ...` to inspect freshness, safety,
manual provenance, and conflicts.

## Capability measurement

```text
puckdb draft session capability --league 500.l.5621
```

The report combines the imported league's actual draft format, team count,
scoring type, pick clock, and status with retained poll observations. It reports
request-duration distribution, authentication failures, HTTP 429s,
partial/empty payloads, completion, and the largest poll-to-change detection
gap. Yahoo's `draftresults` payload has no per-pick publication timestamp, so
true upstream publication latency is unsupported; the detection gap is only an
upper bound determined by polling.

No live/mock draft observation for the target 2026 leagues is tracked in this
repository. Run the capability command during an available draft before
tightening the polling interval or claiming a Yahoo update-latency target.
