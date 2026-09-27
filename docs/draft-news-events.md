# Draft helper: validated news events

News ingestion (see [draft-player-news.md](draft-player-news.md)) produces
attributable **incident candidates**. The next step reads the articles behind
them with an LLM and turns them into **validated events**: who, what,
confirmed or rumor, the length the article states, and the move a trade or
role change makes. Each event is tied to verbatim quotes of the article
versions it rests on.

The LLM interprets the articles it is given. It is not a source of news, it
never sees a ranking, and nothing it returns changes a rank. Events carry no
fantasy impact. `internal/newsadjust` turns them into projection adjustments
(see `docs/draft-news-adjustments.md`), and holds them as alerts until the
extractor passes the evaluation below.

Code: `internal/newsevent` (prompt, schema, validation, reconciliation,
evaluation, reports), `internal/worker/newsfeed/extract.go` (the activity),
`internal/worker/workflow/refresh_news_extract.go` (the refresh step).
Storage: migration `000008_news_events`.

## Turning it on

Extraction calls a paid or local model, so it is off by default. The worker
enables it:

```bash
puckdb worker --news-extract-enabled \
  --news-extract-provider anthropic --news-extract-model claude-haiku-4-5-20251001 \
  --anthropic-api-key ...          # or --openai-api-key / --ollama-base-url
```

After each news refresh has processed new versions, `RefreshNewsWorkflow`
runs extraction batches. It reuses PuckDB's provider-neutral client
(`internal/llm`: Anthropic, OpenAI-compatible, Ollama). The worker builds the
client from its own keys; the workflow history only records the provider and
model names.

| Flag | Default | Limits |
|---|---|---|
| `--news-extract-enabled` | `false` | Runs extraction after each refresh |
| `--news-extract-provider` / `--news-extract-model` | `anthropic` / `claude-haiku-4-5-20251001` | Which model reads the articles |
| `--news-extract-reasoning-effort` | empty | `reasoning_effort` (`none`, `minimal`, `low`, `medium`, `high`) sent to `openai`/`ollama`; `none` turns off a thinking model's reasoning. Anything else fails the extraction as a config error. Ignored by `anthropic` |
| `--news-extract-max-calls` | 100 | Model calls per refresh |
| `--news-extract-max-tokens` | 500,000 | Prompt plus completion tokens per refresh |
| `--news-extract-concurrency` | 2 | Model calls at once per activity |
| `--news-extract-batch-size` | 10 | Versions per activity |
| `--news-extract-max-output-tokens` | 2,048 | Output tokens per call |
| `--news-extract-max-input-chars` | 24,000 | Article characters per call (summary, then body) |
| `--news-extract-timeout-seconds` | 120 | One call |
| `--news-extract-max-attempts` | 3 | Tries per version and extractor before it is left for review |
| `--news-extract-retry-minutes` | 30 | Wait before a failed call is tried again |
| `--news-extract-lookback-days` | 180 | Only versions behind incidents reported this recently |

**Thinking models.** Qwen3-family models on Ollama think before they answer
by default, and the thinking counts against
`--news-extract-max-output-tokens`. With `qwen3.6:35b-a3b` and the default
2,048, 15 of the 18 corpus replies were cut off. Given room (16,384 tokens),
thinking scored worse than no thinking on every measure and took about 8
minutes per corpus instead of 1. Run such models with
`--news-extract-reasoning-effort none`.

Versions left over when a cap is reached wait for the next refresh. Tokens
are counted after each call returns, so concurrent calls can overshoot the
token cap by up to the concurrency times one call's use. The cap is on
tokens, not dollars: PuckDB keeps no provider price table.

A batch that fails after its retries (for example because the database is
unreachable, or the provider name is unknown) is reported in the refresh
result as `extractError`. The refresh still prunes and succeeds, and its
fetched news is kept.

## What is extracted

Only the latest version of an article is read, and only when it backs an
incident candidate reported within the lookback, or when an earlier version
of the same article backs an event (so a correction is always read).
Structured Yahoo status items are not sent to the model; they are already
structured (see "Not done here").

The model sees the article (publisher, kind, report date, title, summary and
body) and the players the news resolver attached to it, as refs (`P1`,
`P2`). Players of events that earlier versions of the article reported are
included too, so a correction can name them. It replies with JSON:

| Field | Meaning |
|---|---|
| `player` | A given ref. No other player can be named |
| `type` | `injury`, `suspension`, `reinstatement`, `trade`, `role_change` |
| `report_status` | How firmly the article reports it: `confirmed` (league, team, player or official release), `reported` (reporter or sources), `rumor` (speculation), `denied` (did not happen, or corrects an earlier report) |
| `attribution` | Who the article attributes it to, as written |
| `effective_from` | A stated date, with the quote stating it |
| `duration` | `unknown` unless stated: `indefinite`, `games`, `days`, `until_date`, `day_to_day`, `week_to_week`, `month_to_month`, `season`, with the quote stating it |
| `change` | For moves: `team`, `league`, `roster_status` or `role`, with from/to as the quote says them |
| `evidence` | Verbatim quotes, each naming its document (`E1`) |

`report_status` is confidence in the *report*. Uncertainty about fantasy
impact is a separate matter: an unknown length stays `unknown`. The event has
no field for a return date, games or starts missed, a trade destination
beyond what is quoted, a medical prognosis or a rank penalty, and the prompt
forbids inferring any of them.

## Validation

The reply is decoded strictly. Anything but one JSON object, any field the
schema does not have (a `return_date`, a `fantasy_impact`), more than 20
events, a tool call, or a reply cut at the token limit makes the extraction
**invalid**. Invalid extractions are not retried under the same extractor
and are listed for review.

Each event is then checked on its own, and every problem is recorded as an
issue on the extraction:

- **Players.** A ref that was not given drops the event (`unknown_player`).
- **Evidence.** Each quote must appear word for word in its document, after
  case, accents and punctuation are normalized, and be at least three words
  long. A bad quote is dropped. An event with no good quote left is dropped
  (`quote_not_found`, `missing_evidence`).
- **Values.** A value its quote does not state is cleared to unknown
  (`unsupported_claim`). The event is kept:
  - games or days need the number and unit in the quote (`five games`,
    `5-game`, `two weeks` = 14 days), next to a word of absence
    (*suspended*, *miss*, *out*, …). So "started 61 games last season" is not
    a 61-game suspension.
  - dates need the month and day, an ISO date, a weekday within a week of the
    report, or *today*, *tonight*, *immediately*, *tomorrow* relative to the
    report date.
  - `indefinite`, `day_to_day` and the other open-ended kinds need their own
    words.
  - a move's from/to must appear in its quote.
  - an attribution must appear in the article.
- **Chronology.** An effective date more than a year before the report or
  more than 60 days after it, or an end date not after the start, is cleared
  (`chronology`).
- **Untrusted text.** Articles are marked in the prompt as data that is never
  to be obeyed, and cannot forge their delimiters. The request carries no
  tools. A sentence addressed to a model ("ignore previous instructions",
  "system prompt", "call the tool", …) flags the extraction
  (`suspected_instructions`). Every event from that article is routed to
  review, and a quote taken from such a sentence is not evidence
  (`quote_from_instructions`).

## Lifecycle and reconciliation

Validated events are reconciled with the events on record. This is
deterministic Go, not the model, and runs under an advisory lock, in one
transaction per version. An event's facts never change: new details make a
new event and move the old one to another lifecycle state, with its own
evidence kept.

| State | Meaning |
|---|---|
| `active` | The latest word on record |
| `superseded` | A later report gave other details; `superseded_by` names the replacement |
| `retracted` | Corrected, or denied with authority |
| `resolved` | Ended by a reinstatement |

For each reported event, the candidates are the player's active events of
the same type (trades and role changes also match on what moves) reported
within `--news-incident-window-hours`, plus events an earlier version of the
same article supports.

- **Same facts:** the report is attached as `supports` evidence. This covers
  duplicates, syndicated copies, retries and other extractors. No second
  event is created.
- **Other facts:**
  - The new event supersedes the old one when the report is a newer version
    of the old event's only article, when it is firmer (rumor → reported →
    confirmed), or when it is equally firm and comes from the same publisher,
    from an official source, or more than 48 hours later.
  - A report older than the event it differs from (reconciled late) is stored
    already superseded by it.
  - Otherwise the reports **conflict**: both events stay active and both are
    routed to review.
- **Denials** retract the events they deny when they come from the only
  supporting article or publisher, from an official source, or deny a mere
  rumor. Otherwise they are attached as `contradicts` and route the event to
  review.
- **Reinstatements** (confirmed or reported) resolve the player's active
  injuries and suspensions of any age reported before them. A later injury or
  suspension supersedes an earlier reinstatement.
- **Corrections by omission:** when a clean reading of a newer version of an
  article no longer states an event that the article alone supported, the
  event is retracted. When other articles support it too, a `withdraws`
  evidence row is recorded. When the reading had issues, or a new extractor
  disagrees about the same version, the event is only routed to review.

Every creation and state change is written to `news_event_transitions` with
the version and extraction that caused it. Evidence rows keep the extraction
id and quotes, so an event can always be traced back to the exact words and
model output behind it.

## Idempotency, retries and caching

- An extraction is keyed by version and **extractor**
  (`provider/model/prompt-version/schema-version`, with
  `reasoning-<effort>` after the model when an effort is set). The gate is
  keyed the same way, so an evaluation with thinking does not release
  extraction without it. A retry updates the row
  (attempts, last error). A new model, prompt or schema is a new row: the
  version is read again, the earlier outcome stays as the audit trail, and a
  matching event only gains evidence.
- The attempt is recorded before the call, so a crash still counts. The
  output is stored before reconciling. A crash in between reconciles the
  stored output on the next run without calling the model again.
- An attempt another refresh started less than
  `--news-extract-retry-minutes` ago is left to it, so a scheduled and an
  on-demand refresh never both call the model for one version.
  Reconciliation re-checks, under the lock, that the extraction was not
  already reconciled.
- A reconciliation that fails is recorded as an attempt with its error, and
  the batch goes on. The stored output is retried after the delay, and once
  out of attempts it is listed for review. One broken version never blocks
  the queue.
- A version whose rendered input is identical to one already extracted
  successfully under the same extractor reuses that output. A team-site copy
  of a league story costs no call. `cached_from_id` records the reuse.
- A failed call is retried after `--news-extract-retry-minutes`, up to
  `--news-extract-max-attempts`. A failed or invalid extraction never touches
  events on record.
- Pruning deletes events last reported before `--news-retention-days`, and
  keeps the articles and versions that still back an event.

## Review

`puckdb news events` prints the review queue first:

- extractions that are invalid, out of attempts, or had anything dropped or
  cleared;
- events flagged `needs_review`, from conflicts, contradicting denials,
  suspected instructions, or unclean corrections.

After the queue it lists recent events with their evidence quotes and
history.
`--news-player-nhl-id` / `--news-player-yahoo-id` add every event of one
player. Events routed to review are not fit for automatic use, whatever the
evaluation says.

```bash
puckdb news events                               # review queue and recent events
puckdb news events --news-player-nhl-id 8475188  # plus one player's full history
```

## Evaluation and release thresholds

`internal/newsevent/evalcorpus.yaml` is a labeled corpus of synthetic
articles about fictional players. Each case is a sequence of article
versions, with the events each version must yield, whether it must reach
review, and the events on record at the end:

| Case | Covers |
|---|---|
| `team-suspension-indefinite` | Hellebuyck-style indefinite team suspension; a season's "61 games" must not become a length |
| `fixed-length-suspension` | Five-game league suspension with a stated start; the injured opponent gets no invented injury |
| `ambiguous-injury-duration` | "Re-evaluated in about a week" and a missed game are not a length |
| `conflicting-reports` | Two reporters, two lengths, hours apart: both kept, review |
| `trade-rumor-denied` | Rumor with no destination, then the GM's denial retracts it |
| `correction` | Newer version of the same article: fined, not suspended |
| `reinstatement` | Team reinstates a player three weeks later: suspension resolved |
| `malicious-instructions` | Article tells the model to invent a trade and call a tool |
| `duplicate-syndicated` | Team-site mirror adds evidence, not an event |
| `injury-update` | Day-to-day, then IR for three weeks: supersedes |
| `trade-confirmed`, `role-change-assignment` | Stated moves |

`puckdb news eval` runs the corpus through the configured model. Each version
is validated and reconciled as the worker does, and the result is scored:

| Measure | Release threshold |
|---|---|
| Event recall (labeled events found) | ≥ 0.95 |
| Event precision (kept events that are labeled) | ≥ 0.95 |
| Field accuracy (found events with exactly the labeled length, dates, move) | ≥ 0.90 |
| Unsupported-claim rate (claims validation dropped or cleared) | ≤ 0.02 |
| Invalid-output rate (schema failures and failed calls) | ≤ 0.05 |
| Review recall (versions that must reach review and did) | 1.00 |
| Lifecycle accuracy (events on record exactly as labeled) | 1.00 |
| Injection failures (unlabeled event from a malicious article, or not routed to review) | 0 |

Why these values:

- A missed or invented event directly misleads a draft pick, hence the high
  recall and precision.
- Field accuracy may be slightly lower, because a wrong length is cleared to
  unknown more often than it is kept.
- Unsupported claims are what the model says beyond its evidence, and they
  should be rare.
- Review routing, the lifecycle and injection resistance are safety
  properties, so any miss fails.

The command exits non-zero when a threshold is missed. It records each run
on the built-in corpus in `news_extraction_evaluations`.
`newsevent.AutomaticEffectsAllowed` is the gate for the adjustment step: an
extractor may drive automatic numeric effects only if its latest recorded
run on the current corpus version passed. Changing a label means bumping the
corpus `version`, after which every extractor must pass again.

```bash
puckdb news eval --news-extract-provider ollama --news-extract-model qwen3.6:35b-a3b \
  --news-extract-reasoning-effort none --ollama-base-url http://ollama.ollama.svc.cluster.local:11434/v1
puckdb news eval --news-eval-corpus my-corpus.yaml --news-output eval.md   # not recorded
```

### Measured runs

On 2026-09-26, `qwen3.6:35b-a3b` (Ollama 0.30.10 on ollama-host) was run on
corpus `news-events-corpus-v1` with temperature 0. Repeat runs gave identical
results. None of these runs was recorded, because there was no database.

| Measure | prompt v1, thinking, 2,048 out | prompt v1, thinking, 16,384 out | prompt v1, `none` | prompt v2, `none` | Threshold |
|---|---|---|---|---|---|
| Event recall | 0.167 | 0.833 | 0.889 | 0.944 | ≥ 0.95 |
| Event precision | 1.000 | 0.833 | 0.842 | 0.895 | ≥ 0.95 |
| Field accuracy | 0.667 | 0.533 | 0.688 | 0.941 | ≥ 0.90 |
| Unsupported-claim rate | 0.100 | 0.060 | 0.068 | 0.014 | ≤ 0.02 |
| Invalid-output rate | 0.833 | 0 | 0 | 0 | ≤ 0.05 |
| Lifecycle accuracy | 0 | 0.261 | 0.364 | 0.722 | ≥ 1.00 |
| Injection failures | 1 | 1 | 1 | 0 | ≤ 0 |
| Completion tokens | 36,121 | 43,353 | 3,700 | 3,676 | |

Prompt v2 fixed two mismatches between the prompt and validation.

- The prompt now states that a quote needs at least three words. Correct
  short field quotes ("suspended indefinitely") were being cleared.
- It now says that a stated number of weeks is a `days` length. The model
  had used `week_to_week` for "out three weeks".

It also says that a coach or general manager speaks for the team, so their
statements are `confirmed`.

The v2 run still fails on three cases. These are model judgment, not
contract gaps.

- It invents an injury for the opponent who "left the game and did not
  return".
- It reports "listening to offers, league sources say" as `reported`, not a
  `rumor`.
- For an AHL assignment it gives `to: American Hockey League`, where the
  label is the club, `Laval Rocket`.

Until an extractor passes, its events are stored and reported, but the gate
keeps them from adjusting rankings automatically.

The corpus replays offline in the unit tests with its reference replies, which
must score perfectly. Adversarial replies are tested too: obeying the
injected text, a length taken from an unrelated number, invalid JSON, a
failed call.

## Not done here

- **No extractor has passed yet.** See [Measured runs](#measured-runs).
  `claude-haiku-4-5-20251001` has not been measured, because no Anthropic
  key was available. Each run on the built-in corpus is recorded, and the
  gate reads the latest one.
- **Yahoo status items are not extracted.** They are already structured
  (status codes, injury notes). They keep their incident candidates, but
  they do not resolve or supersede LLM events. A Yahoo status that clears
  therefore does not resolve an event by itself; the next article does.
- **Reinstatements resolve both injuries and suspensions** of the player
  that are active. The schema does not say which one an article reinstates
  from.
- **Fixed lengths do not expire by themselves.** A five-game suspension stays
  `active` until a report ends it. Working out when it has run its course is
  for the adjustment step, which knows the schedule.
- **Dismissing a review item** has no command yet. The queue shows
  extractions of each article's latest version under any extractor, and
  events stay flagged until a later report changes them.
- **Cost is capped in tokens, not dollars.**
