# Maurice user and usage schema proposal

Status: proposal only. This document does not authorize a migration or an API
change.

## Goals

Add a local, queryable record of:

- the Authentik identity using Maurice;
- application logins and last login;
- active utilization time;
- conversation prompts, responses, LLM rounds, and tool calls;
- input, output, and prompt-cache token usage per LLM call.

Authentik remains the authentication and identity source. PuckDB stores only
the application identity projection and activity needed for authorization and
analysis. It must not store Authentik passwords, bearer tokens, session cookies,
or JWTs.

## Semantics to settle first

### Identity

Use Authentik's `X-authentik-uid` as the external subject, scoped by an
`auth_provider` value. Authentik documents this header as the hashed identifier
of the authenticated user. Usernames, display names, and email addresses can
change and must not be keys. The reverse proxy must remove client-supplied
identity headers and be the only trusted source of them.

Reference: [Authentik proxy-provider headers](https://docs.goauthentik.io/add-secure-apps/providers/proxy/#headers-sent-to-upstream-applications).

### Login count

Forward auth runs on requests; it does not tell PuckDB that Authentik has just
performed an interactive login. Counting authenticated requests would produce
an invalid `login_count`.

The proposed local meaning is **one login per PuckDB application session**. On
the first authenticated request without a valid PuckDB session cookie, PuckDB
creates a session, sets an opaque cookie, and records `started_at`. Repeated
requests in that session do not increment the count.

If the required metric is instead "successful Authentik authentication
events", it needs an Authentik event/webhook ingestion path with the Authentik
event ID as an idempotency key. That is a separate integration and is not
represented as ordinary application sessions.

### Utilization time

Elapsed session time exaggerates utilization when a browser tab is idle. The
proposal measures active time in bounded heartbeat buckets. Each client
heartbeat upserts the active seconds for one session and time bucket; retrying
the heartbeat cannot add time twice. The bucket duration, heartbeat frequency,
and maximum credited gap should be named application constants decided during
implementation.

LLM latency is a different metric and is stored per LLM call. Reports must not
present model latency as user utilization time.

### Token counts

Store the provider's counts per LLM call, including failed turns whose provider
call succeeded. Use nullable columns because "provider did not report usage" is
not the same as zero tokens. Do not derive input tokens by tokenizing stored
text later: provider tokenizers and cache accounting differ.

`input_tokens` and `output_tokens` use the provider-neutral meanings already
exposed by `llm.Usage`. Cache-creation and cache-read input tokens remain
separate because Anthropic reports them separately. A reporting view may define
a billed-input total according to the provider's pricing semantics; the write
path should preserve the raw reported values.

## Relationships

```text
app_users
  |--< app_sessions --< app_session_activity
  |--< maurice_conversations --< maurice_turns --< maurice_messages
                                      |              ^
                                      |              |
                                      |--< maurice_llm_calls
                                      |       |--< maurice_llm_call_messages >--|
                                      |       `--< maurice_tool_calls
                                      `-- request idempotency boundary
```

The existing `maurice_conversations` and `maurice_messages` tables remain the
canonical transcript. New tables add ownership, turn boundaries, and analysis
data rather than replacing the transcript with an analytics-only event log.

## Proposed tables

The column lists below are the proposed logical schema, not migration SQL.
Names and constraints should be finalized together with the write-path design.

### `app_users`

Local projection of an external identity.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `id` | `uuid` | Primary key, generated locally |
| `auth_provider` | `text` | Not null; initially `authentik` |
| `auth_subject` | `text` | Not null; value from `X-authentik-uid` |
| `username` | `text` | Not null; mutable profile snapshot |
| `display_name` | `text` | Nullable; mutable profile snapshot |
| `first_seen_at` | `timestamptz` | Not null |
| `profile_updated_at` | `timestamptz` | Not null |
| `disabled_at` | `timestamptz` | Nullable local access/retention state |

Constraints and indexes:

- unique `(auth_provider, auth_subject)`;
- optional non-unique index on `username` for administration only;
- no unique constraint on username or email.

Only store email if a concrete product requirement needs it. It is not needed
for identity linkage or the requested usage statistics.

### `app_sessions`

One locally observed application login.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `id` | `uuid` | Primary key |
| `user_id` | `uuid` | Not null, FK to `app_users(id)` |
| `session_key_hash` | `bytea` | Not null; keyed digest of the opaque cookie value |
| `started_at` | `timestamptz` | Not null; the local login time |
| `last_activity_at` | `timestamptz` | Not null |
| `ended_at` | `timestamptz` | Nullable; logout or expiry |

Constraints and indexes:

- unique `session_key_hash`;
- index `(user_id, started_at DESC)`;
- check `ended_at IS NULL OR ended_at >= started_at`.

The raw cookie value is never stored. Rotation creates a new session only when
the product intends to count a new login, not on every cookie refresh.

### `app_session_activity`

Retry-safe active-time buckets for a session.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `session_id` | `uuid` | FK to `app_sessions(id)` with delete cascade |
| `bucket_started_at` | `timestamptz` | Start of the normalized activity bucket |
| `active_seconds` | `integer` | Non-negative time credited inside the bucket |
| `last_observed_at` | `timestamptz` | Not null; latest heartbeat included |

Primary key: `(session_id, bucket_started_at)`.

An upsert sets `active_seconds` to the greatest previously stored or newly
computed value for the bucket. It must not add the submitted value, because a
retried heartbeat would inflate utilization.

### Changes to `maurice_conversations`

Add:

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `user_id` | `uuid` | FK to `app_users(id)`; owner of the conversation |

Add index `(user_id, updated_at DESC)`. All get, list, update, and delete
queries must include `user_id`; knowing a conversation UUID is not
authorization. An explicitly authorized administrator query can be separate.

For existing rows, add the column as nullable, execute an explicit ownership or
legacy-retention decision, and only then make it not null. Do not silently
assign historical conversations to the first user who requests them.

### `maurice_turns`

One user prompt and the complete work needed to produce its final response.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `id` | `uuid` | Primary key |
| `conversation_id` | `uuid` | FK to `maurice_conversations(id)` with delete cascade |
| `turn_number` | `integer` | Zero-based order within the conversation |
| `idempotency_key` | `text` | Client-generated key for safe request retry |
| `status` | `text` | `running`, `succeeded`, `failed`, or `cancelled` |
| `started_at` | `timestamptz` | Not null |
| `completed_at` | `timestamptz` | Nullable until terminal |
| `error_class` | `text` | Nullable, bounded classification without secret-bearing details |

Constraints and indexes:

- unique `(conversation_id, turn_number)`;
- unique `(conversation_id, idempotency_key)`;
- index `(conversation_id, started_at)`;
- terminal states require `completed_at`;
- `completed_at` cannot precede `started_at`.

The request idempotency key prevents a browser retry from producing duplicate
prompts, token charges, and responses. A request with the same key and a
different prompt must be rejected.

### Changes to `maurice_messages`

Keep the existing role, content, `tool_calls`, and `tool_call_id` columns. Add:

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `turn_id` | `uuid` | Nullable only for legacy or conversation-level messages; FK to `maurice_turns(id)` |
| `message_number` | `integer` | Exact order within a turn |

Add unique `(turn_id, message_number)` when `turn_id` is not null. Continue to
index `(conversation_id, created_at, id)` for whole-conversation display, using
`id` as a deterministic tie-breaker.

User-role rows are the canonical stored prompts. Assistant and tool rows keep
the complete transcript in the format Maurice already reloads. The message
content should not be copied into summary tables.

### `maurice_llm_calls`

One provider completion request. A turn can contain multiple tool-loop rounds
and a forced-final call. Title generation is also billable and may be recorded
with `turn_id` null and `conversation_id` set.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `id` | `bigint` | Identity primary key |
| `turn_id` | `uuid` | Nullable FK to `maurice_turns(id)` with delete cascade |
| `conversation_id` | `uuid` | FK to `maurice_conversations(id)` with delete cascade |
| `call_kind` | `text` | `chat_round`, `forced_final`, or `title_generation` |
| `round_number` | `integer` | Nullable only for title generation; zero-based within a turn |
| `provider_request_id` | `text` | Nullable provider identifier; unique per provider when present |
| `provider` | `text` | Not null |
| `model` | `text` | Not null; actual response model when reported |
| `status` | `text` | `succeeded` or `failed` |
| `finish_reason` | `text` | Nullable raw provider finish reason |
| `started_at` | `timestamptz` | Not null |
| `completed_at` | `timestamptz` | Not null |
| `input_tokens` | `integer` | Nullable non-negative provider value |
| `output_tokens` | `integer` | Nullable non-negative provider value |
| `cache_creation_input_tokens` | `integer` | Nullable non-negative provider value |
| `cache_read_input_tokens` | `integer` | Nullable non-negative provider value |
| `error_class` | `text` | Nullable bounded classification |
| `system_prompt` | `text` | Exact system prompt sent for this call |
| `tool_definitions` | `jsonb` | Exact provider-neutral tool definitions sent, or null |

Constraints and indexes:

- unique `(turn_id, round_number, call_kind)` when `turn_id` is not null;
- index `(turn_id, round_number)`;
- index `(conversation_id, started_at)`;
- all reported token counts are non-negative;
- `completed_at >= started_at`.

Duration is derived from the two timestamps. `system_prompt` and
`tool_definitions` deliberately snapshot the inputs that code or MCP servers
can change over time. If storage volume warrants deduplication later, move them
to content-addressed snapshot tables without changing their meaning.

### `maurice_llm_call_messages`

Records the ordered transcript messages supplied to each provider call without
duplicating their content.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `llm_call_id` | `bigint` | FK to `maurice_llm_calls(id)` with delete cascade |
| `input_number` | `integer` | Zero-based order in the provider request |
| `message_id` | `uuid` | FK to `maurice_messages(id)` with delete cascade |

Primary key: `(llm_call_id, input_number)`. Also enforce unique
`(llm_call_id, message_id)`.

Together with `system_prompt` and `tool_definitions`, this table reconstructs
the provider-neutral prompt for every round while keeping message content in
one place. The current turn's messages and these references must be written in
one transaction after the tool loop completes.

### `maurice_tool_calls`

Structured analysis record for each MCP call. The corresponding assistant and
tool messages remain in `maurice_messages` for replay.

| Column | Type | Constraints and meaning |
| --- | --- | --- |
| `id` | `bigint` | Identity primary key |
| `turn_id` | `uuid` | FK to `maurice_turns(id)` with delete cascade |
| `llm_call_id` | `bigint` | FK to the requesting `maurice_llm_calls(id)` |
| `sequence_number` | `integer` | Order among calls requested by that LLM response |
| `provider_tool_call_id` | `text` | ID supplied by the LLM provider |
| `tool_name` | `text` | Not null |
| `arguments` | `jsonb` | Parsed arguments when valid |
| `arguments_raw` | `text` | Original arguments for parse-failure analysis |
| `result` | `text` | Tool response or bounded failure text |
| `status` | `text` | `succeeded`, `tool_error`, `parse_error`, or `cancelled` |
| `started_at` | `timestamptz` | Not null |
| `completed_at` | `timestamptz` | Not null |

Constraints and indexes:

- unique `(turn_id, provider_tool_call_id)`;
- unique `(llm_call_id, sequence_number)`;
- index `(tool_name, started_at)` for tool-level analysis;
- `completed_at >= started_at`.

Tool arguments and results can contain more sensitive data than ordinary chat
messages. They need the same access control and deletion policy as prompts.

## Derived user statistics

Expose statistics through a view or reporting query rather than mutable
counters on `app_users`:

```sql
SELECT
    u.id AS user_id,
    count(DISTINCT s.id) AS login_count,
    max(s.started_at) AS last_login_at,
    coalesce(sum(a.active_seconds), 0) AS active_seconds,
    coalesce(sum(c.input_tokens), 0) AS input_tokens,
    coalesce(sum(c.output_tokens), 0) AS output_tokens,
    count(DISTINCT t.id) FILTER (WHERE t.status = 'succeeded') AS successful_turns
FROM app_users AS u
LEFT JOIN app_sessions AS s ON s.user_id = u.id
LEFT JOIN app_session_activity AS a ON a.session_id = s.id
LEFT JOIN maurice_conversations AS conversation ON conversation.user_id = u.id
LEFT JOIN maurice_turns AS t ON t.conversation_id = conversation.id
LEFT JOIN maurice_llm_calls AS c ON c.turn_id = t.id
GROUP BY u.id;
```

This is illustrative, not final view SQL: joining several one-to-many tables
directly can multiply rows. The implementation should aggregate sessions,
activity, turns, and token usage in separate subqueries before joining them.
The example documents the intended outputs and source tables.

For dashboards over large histories, add daily aggregate tables only after
query volume demonstrates a need. Raw rows remain the source of truth, and
aggregate refreshes must be idempotent.

## Write boundaries

1. Auth middleware upserts `app_users` from trusted headers and resolves or
   creates `app_sessions`.
2. The Maurice resolver passes the resolved user and a client idempotency key
   into the service. Existing conversation IDs are accepted only when owned by
   that user.
3. The service creates or resumes a `maurice_turns` idempotency record.
4. Each provider response is retained in memory as an LLM-call record; each
   tool execution is retained as a tool-call record.
5. On a completed turn, messages, call rows, call-message links, tool calls,
   and the terminal turn state commit together with the conversation timestamp.
6. If a turn fails after a provider has returned usage, persist the failed turn
   and incurred call usage without adding a partial transcript that Maurice
   could replay as valid history.

Step 6 needs a deliberate transaction path because the current service
correctly persists successful turns all-or-nothing. Analytics must not weaken
that transcript guarantee merely to retain failure telemetry.

## Retention and privacy requirements

- Prompts, tool arguments, and tool results can contain personal information,
  private league data, and secrets. They require explicit access control,
  encrypted transport/backups, and a documented retention duration.
- A user's conversation deletion should cascade through raw messages, turns,
  LLM calls, and tool calls. Decide separately whether anonymized daily usage
  aggregates may remain.
- User deletion needs an explicit policy: hard-delete all owned content or
  replace the identity with an irreversible analytics subject. Do not retain a
  reversible email/username mapping in an "anonymous" row.
- Error fields must store bounded classifications. Raw provider errors can
  contain submitted content or credentials and should remain in protected
  operational logs with their own retention policy.
- Application authorization must be enforced before this schema is exposed to
  multiple users. The existing Maurice conversation queries are not currently
  owner-scoped.

## Rollout order for a later implementation

1. Confirm whether the desired login count means local PuckDB sessions or
   literal Authentik authentication events.
2. Confirm active-time heartbeat semantics and retention periods.
3. Add trusted `X-authentik-uid` propagation and user/session resolution.
4. Add nullable ownership and turn linkage, then explicitly backfill or remove
   legacy PostgreSQL conversations.
5. Make ownership mandatory and update every conversation query to scope by
   user.
6. Add per-call usage, exact prompt references, and tool-call telemetry.
7. Add derived reporting views and only then consider daily aggregates.

The local SQLite Maurice REPL is a separate single-user store. It does not need
`app_users` or Authentik sessions unless the product later requires its local
history to synchronize with the API. Shared transcript behavior should still
remain compatible across the SQLite and PostgreSQL adapters.

## Open decisions

- Does `login_count` mean a local PuckDB session or a successful Authentik
  authentication event?
- What heartbeat interval, idle cutoff, and bucket size define active use?
- How long may raw prompts and tool payloads be retained?
- Should title-generation LLM calls count toward a user's token usage?
- Should failed turns preserve exact provider prompts, or only usage and error
  classifications?
- Who may query per-user analytics, and may users export or erase their data?
- What ownership or deletion rule applies to pre-user-schema conversations?
