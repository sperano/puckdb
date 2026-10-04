# Maurice live draft board

The PuckDB API serves the browser board at `/draft/`. It records and explains
draft decisions; it never submits a selection to Yahoo. The two configured
2026 league IDs are available from the league selector, and a full Yahoo league
key may be used through GraphQL.

## Transport and ordering

The browser polls `mauriceDraftBoard` every two seconds. Yahoo itself is polled
by the backend watch at the configured draft-watch interval (30 seconds by
default), with the bounded backoff and final-reconciliation behavior described
in [Yahoo draft watch](yahoo-draft-watch.md). This separates cheap local reads
from authenticated upstream requests.

Every response has two monotonic versions:

- `stateVersion` changes only when a Yahoo pick, local edit, undo, or conflict
  resolution changes the effective board.
- `syncVersion` changes for every Yahoo poll outcome and committed local
  mutation, including identical polls and failures that only affect freshness.

Clients discard a response with an older `syncVersion`. A gap in
`stateVersion` means events were missed during a disconnect; the board query is
a complete snapshot, so the client replaces its local view and may request
`mauriceDraftEvents(afterStateVersion:)` for a bounded audit trail. Reloading
never reconstructs state from the browser: confirmed Yahoo picks, local
overlays, tombstone undos, conflicts, and the shortlist all live in PostgreSQL.
An undo tombstone stays visible as a recovery row whenever a later Yahoo
correction conflicts with it, so either side can be selected explicitly.

## Recovery controls

`startMauriceDraftWatch` is idempotent within one API process. The database
advisory lock still prevents a CLI or another API process from watching the
same league concurrently. `stopMauriceDraftWatch` cancels the watch and waits
for its bounded final reconciliation. A process restart stops the in-process
watch; the persisted board remains intact and the user can start watching
again.

Manual add, correction, undo, and conflict-resolution mutations require the
`stateVersion` the user saw. The row-locked write rejects a stale version rather
than silently rebasing a click onto a newer Yahoo board. The browser disables
action controls while a mutation is pending and supplies a unique client action
identifier, preventing accidental double clicks. Local entries and conflicts
remain visibly distinct from Yahoo-confirmed picks.

## Freshness, recommendations, and timing

The available list always comes from one immutable ranking snapshot and removes
every effective drafted player. Roster-fit recommendations use the same session
version and are saved with the ranking, projection, rule, and news versions
that produced them. News reports display their evidence date; a newer ranking
snapshot can replace the list on a later poll without blocking the persisted
draft board.

Unsafe, incomplete, conflicted, or stale sessions keep the board visible but
suppress recommendations and show a prominent warning. Position filters do not
renumber baseline ranks or hide the independent best-value and roster-fit
recommendation details. The UI displays Yahoo's configured per-pick time as a
limit, not a countdown. For snake drafts, the current pick and
turns-to-selection can be estimated when every imported team has one distinct
Yahoo draft position; the service expands those positions across the
non-reserve roster rounds for standard live or snake drafts. The UI labels
that value as an estimate and keeps `orderKnown` false because imported team
positions do not prove traded-pick ownership. Only a supplied per-pick order is
treated as verified. Auctions, unknown formats, and incomplete positions omit
the estimate.

## Keyboard and responsive use

In the available-player table, Up/Down changes the focused player, `S` toggles
the shortlist, and Enter opens the recommendation and dated-news details. The
desktop layout keeps the decision queue beside recommendations, roster,
shortlist, and history. Below 900 pixels it becomes one column while retaining
all controls and table scrolling.
