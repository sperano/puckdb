# Draft helper: player news and status updates

The draft helper needs to know about suspensions, injuries, reinstatements,
trades and role changes before a pick is made, and needs to know where that
information came from. This note describes the sources PuckDB reads, what it
stores, how it groups repeated reports, how it attaches stories to players,
and how it makes missing or stale coverage visible.

Nothing here ranks or penalizes a player. Ingestion produces attributable
**incident candidates**. The next step (validated event extraction) reads
them, and only then does anything reach a ranking.

## Sources

The built-in set is `internal/news/sources.yaml`. Every source is free and
needs no account. `--news-sources-file` replaces the set with another YAML file
in the same format.

| ID | Publisher | Kind | Adapter | Why |
|---|---|---|---|---|
| `nhl-player-safety` | NHL.com | official | `nhl_content` | Department of Player Safety suspensions and fines |
| `nhl-injury` | NHL.com | official | `nhl_content` | League and team-site stories tagged *injury* |
| `nhl-transactions` | NHL.com | official | `nhl_content` | Team announcements of trades, assignments, recalls, waivers |
| `nhl-press-releases` | NHL.com | official | `nhl_content` | Press releases (signings, reinstatements, roster moves) |
| `yahoo-status` | Yahoo Fantasy | structured | `yahoo_status` | Status code, injury note and IR flag from the imported league player pools |
| `rotowire-nhl` | RotoWire | reporting | `rss` | Free player-news RSS; each blurb names the original reporter |
| `sportsnet-nhl` | Sportsnet | reporting | `rss` | NHL RSS; supports ETag and Last-Modified |
| `espn-nhl` | ESPN | reporting | `rss` | Disabled: mostly features and previews |

- **NHL content** is the public content API behind nhl.com and the team sites
  (`forge-dapi.d3.nhle.com`). Like the stats API PuckDB already uses, it is
  unauthenticated and undocumented. Stories carry structured tags: NHL player
  IDs (`playerid-…`) and team abbreviations. Team-site mirrors of league
  stories are separate entries with the same text.
- **Yahoo status** does not call Yahoo. It reads the `yahoo_league_players`
  rows the Yahoo sync imported for the season. A player listed in both target
  leagues (1001 and 1002) is one fact, taken from the newest pool fetch, and
  is shared by both leagues. The data is only as current as the oldest
  league's last pool import, and coverage reports it that way.
- Each source has a `priority`: lower is more authoritative, and sources are
  fetched and evidence is listed in that order. Priority never adds certainty
  to a report.

Each story keeps its title, the author when given, a link back, and two
texts:

- **Evidence text**: the summary or description the publisher syndicates
  (HTML stripped, at most 1,000 characters). Classification, name matching,
  syndication fingerprints and `news report` read only this.
- **Body**: the full story text, when a source offers more than its summary
  (up to 200,000 characters, paragraphs kept, scripts and styles dropped).
  - NHL content sources set `fetch_body: true`. For each new story, or each
    story whose `lastUpdatedDate` moved, PuckDB downloads the story page (its
    `selfUrl`) and keeps the text of its markdown parts, as markdown. An
    unchanged story reuses the stored body, so a refresh costs one extra
    request per new or updated story only. Downloads stop after two minutes
    per fetch. Stories whose page times out or fails with a network error,
    5xx or 429 are left for the next refresh and not stored yet, and the
    feed's validators are dropped so that refresh reads it again. That lasts
    up to six hours after the story's last update. After that, or when the
    page is gone (404, 403) or unreadable, the story is stored with its
    summary only, so a flaky page cannot keep an official report out.
  - RSS and Atom items keep their full content (`content:encoded`, Atom
    `content`) when it holds more than the summary. Sportsnet includes it for
    some items, often a video embed with no text, which is dropped. Story
    pages of other publishers are never downloaded or scraped.

The body is a local copy of a copyrighted article. It is for this private
database, and nothing republishes it.

On 2026-09-25 every story page of the four NHL.com feeds downloaded (200
pages in about 20 seconds). The pages held about 360 KB of text in all, and
the longest story in each feed was 5–26 KB.

These sources were verified reachable from the sandbox on 2026-09-25. The
default set enables no other source.

## Storage (migrations `000006_player_news`, `000007_news_article_body`)

| Table | Holds |
|---|---|
| `news_fetch_state` | Per source and scope (`feed`, or `season:<year>` for Yahoo status): validators (ETag, Last-Modified, body hash), last attempt, last success, `data_as_of`, last error, consecutive failures |
| `news_articles` | One row per story, keyed by publisher and the publisher's own ID; first and last seen; the source's update time as last seen (decides whether a story page is downloaded again) |
| `news_article_versions` | Every distinct content of a story: title, evidence text, body, author, URL, publication, source-update and retrieval times, content hash, title/text fingerprints, structured subjects and team hints, `processed_at` |
| `news_mentions` | Player references found in a version and how each resolved |
| `news_incidents` | Incident candidates: player (NHL and/or Yahoo ID), category, first and last reported times |
| `news_incident_evidence` | The versions behind each incident, with their relation (see below) |

### Versions: what gets reprocessed

A version's content hash covers the title, evidence text, body, author,
publication time, tagged and body-linked players, team hints and structured
category. The source's update time, the retrieval time and the URL are left
out. An empty body is left out of the hash, so a story stored without one
keeps its hash.

- An **unchanged** story only moves `last_seen_at`. It gets no new version and
  is not reprocessed, even when the source re-stamps it.
- An **updated or corrected** story gets a new version, which is processed
  again. Earlier versions stay in place. If a correction no longer reports the
  event, the report marks the incident as superseded, because every report
  behind it has a newer version that does not support it. A Yahoo status that
  changes or clears is a change of state, not a correction: incidents with
  structured evidence are never marked superseded. A cleared status is
  recorded as a reinstatement instead.
- When nothing changed, fetches are skipped cheaply. PuckDB sends
  `If-None-Match` and `If-Modified-Since` when the source gave validators, and
  compares a hash of the response body otherwise.

## Players

`news.Directory` merges the NHL `players` table with the season's Yahoo pool
players (through `players.yahoo_id`). Names are matched only for active NHL
players and pool players, so a retired namesake cannot capture a rookie's
story.

In order of preference:

1. **Source IDs.** An NHL content player tag (NHL ID) or a Yahoo status (Yahoo
   ID) resolves directly, even for a player PuckDB does not know yet. So does
   a player an NHL.com story body links, either inline
   (`<forge-entity code="player" slug="adam-sykora-8483669">`) or through a
   link to the player's page (`body_nhl_id`).
2. **Full names.** A full name (two to four words; case, accents and
   punctuation ignored) resolves only when exactly one known player has it.
   - When several players share the name, a team the story names (or a team
     the source tags) must pick out exactly one of them. Otherwise the mention
     stays **ambiguous**, with its candidates. Both Elias Petterssons play for
     Vancouver, so a story about "Elias Pettersson" is never attached to
     either.
   - When the only player with the name is not on the team the source tagged,
     the mention is ambiguous (`team_conflict`): it may be a rookie with the
     same name.
3. **Surnames alone are never matched.**
4. A `Name: headline` title (the RotoWire format) whose name matches nobody is
   recorded as **unresolved** (`title_prefix`). An unfamiliar rookie stays
   visible instead of silently disappearing or being attached to someone
   else.

A player is a story's **subject** when the source tags them as its only
player, when the title names them, or when they are the only player the text
names. A player only the body links is a subject when the title names them,
or when no other player is named or tagged. Players who are only mentioned get no incident.

`puckdb news report` lists ambiguous and unresolved subjects for review. An
unmapped NHL/Yahoo pair (the same person present as an NHL-only and a
Yahoo-only record) shows up there as ambiguous; the fix is the NHL↔Yahoo
mapping, which `puckdb draft pool` reports.

## Incidents and repeated reporting

Each version gets one category:

- A structured source's own category, e.g. a Yahoo status code: `DTD`, `O`,
  `IR*` and `GTD` are injury; `SUSP` is suspension; `NA` is role change. A
  status that disappears is a reinstatement.
- Otherwise, keywords in the title, or in the text when the title matches
  none. Rules are tried in order: reinstatement, then suspension, trade,
  injury and role change. So "reinstated from suspension" is a reinstatement.
  A fine is not an incident.

This is a coarse grouping key, not a validated event.

For each resolved subject, the version joins an existing incident of the same
player and category when it was reported within `--news-incident-window-hours`
of that incident (default 14 days, measured from the incident's reported
span), unless a reinstatement reported in between closed the incident.
Otherwise it starts a new incident. Every version of an article joins the
incident its earlier version is on.

Each piece of evidence gets a **relation** to the evidence already attached:

| Relation | Meaning |
|---|---|
| `independent` | First report from its publisher that is not a copy |
| `syndicated` | Same title or text as a report already attached (team-site mirror, wire copy) |
| `same_publisher` | A follow-up from a publisher already attached |
| `revision` | A newer version of an article already attached |

However many reports repeat it, an incident is one row. Only `independent`
reports count as separate sources, and nothing turns the number of reports
into certainty. Downstream steps must apply any effect once per incident,
never once per report.

## Chronology

Each version keeps three times, never merged:

- **published**: the source's publication time
- **updated**: the source's own update time
- **retrieved**: when PuckDB fetched it

A report's time is its publication time, else its update time, else its
retrieval time. Incidents are ordered by it, so a suspension story retrieved
after the reinstatement still comes first. The time an event took effect
(return dates, suspension length) is not extracted here; that is the next
step's job.

## Refreshing

- **On demand:** run `puckdb sync refresh-news` (also part of a full
  `puckdb sync`). Right before a draft, add `--news-force` to fetch every
  source regardless of its refresh interval. `--news-only a,b` limits the
  refresh to the named sources. The GraphQL mutation is
  `refreshNews(input: {force, sources, season})`.
- **Periodically:** start the worker with `--news-schedule-minutes N`. The
  worker creates or updates the Temporal schedule `refresh-news-schedule`,
  which skips a run while another is still running; `0` removes the schedule.
  Each run fetches only the sources whose `refresh_minutes` have passed since
  their last attempt.

`RefreshNewsWorkflow` plans the due sources, then fetches them in parallel.
Fetches retry with exponential backoff (`--news-fetch-max-attempts`,
`--news-fetch-retry-initial`, `--news-fetch-retry-max`), and a server's
`Retry-After` is honored. Network errors, 5xx, 408 and 429 are retried; other
client errors, oversized responses and unparseable feeds are not. The workflow
then processes new versions in transactional batches
(`--news-process-batch-size`) and prunes old news.

Each version is processed in its own transaction, under a PostgreSQL advisory
lock. Once the lock is held, the version is checked again. A scheduled and an
on-demand refresh can therefore run at the same time without creating two
incidents for one event.

**A source that still fails after its retries** is recorded in
`news_fetch_state` (last error, consecutive failures), and the refresh carries
on with the other sources. Its stored stories stay in place, and its last
success time is left untouched, so the report shows the data aging.

**Retention:** articles not seen, and incidents not reported, for
`--news-retention-days` days (default 400, so offseason news is still there at
draft time) are deleted. Articles that still back an incident are kept, as is
the evidence behind every incident. At most `--news-keep-versions` versions
are kept per article; versions that back an incident are always kept.

## Coverage, and why no news is not good news

`puckdb news report` rates every enabled source:

| Status | Meaning |
|---|---|
| `fresh` | The last fetch succeeded and its data is recent |
| `failing` | Recent fetches failed, but the last good data is still recent |
| `stale` | The newest good data is older than `stale_after_minutes` (default three refresh intervals) |
| `missing` | The source never fetched successfully |

Any source that is not fresh or failing gets a warning.

For Yahoo status, recency is measured from `data_as_of`: the oldest league's
latest pool import, not the time PuckDB read the table.

A player with no incident on record is reported as exactly that, followed by
a caveat: "The absence of news is not evidence that the player is healthy or
available." When coverage has gaps, the stale or missing sources are listed
too.

```bash
puckdb news report                                   # coverage, recent incidents, unattached subjects
puckdb news report --news-player-nhl-id 8475188      # plus one player's incidents and assessment
puckdb news report --news-since-days 60 --news-output news.md
```

## Not done here

- **Event extraction.** Dates, durations, rumor versus confirmed, and fantasy
  impact are the next step (LLM extraction with validation). The keyword
  category is only a grouping key.
- **Name resolution limits.** Surname-only mentions are not matched. A player
  whom an NHL.com body links is resolved by NHL ID, but becomes a subject only
  when the title names them. Names in a body are not matched by name, and
  categories come from the title and summary only: a long feature names
  dozens of players and topics that are not its news.
- **RotoWire gaps.** The free RotoWire feed carries only its latest few
  items (five on 2026-09-25). Items published between two polls can be
  missed, so the source is polled every 15 minutes when the schedule runs.
- **Other outlets.** Paid feeds and team-specific beat-writer feeds are not
  included.
