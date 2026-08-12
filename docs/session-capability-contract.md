# Session Capability Contract

Date: 2026-08-11

Contract version: `session.v0.3`.

Status: draft.

This is a lightweight contract sketch, not a wire protocol or OpenAPI-style
schema. It names the minimum outside-visible promises a session service must
make so the rest of an agentic harness can use it without knowing its internals.

`session.v0.3` drops `mutate`, `resume`, `read`, and `materialize` from v0.2.
Writes become dedicated single-purpose actions — `write_message`,
`write_tool_call`, `write_tool_result`, `write_system_note`, `write_record`,
`set_metadata`, `set_usage`. Reads collapse into `get`. State is a first-class
field on every response. Turn grouping is owned by the session service, not by
the caller.

The contract should stay small. A service may be much richer than this, but the
runtime can only assume the base surface below unless the service advertises
more.

## Purpose

The session capability owns conversation continuity.

Every record written to a session is persisted, ordered, and retrievable. The
session service assigns record identities, sequence, timestamps, and turn
grouping. It exposes the full ordered log, session metadata, and usage
accounting. Lifecycle operations — create, get, delete — are the only read
paths. Every write is a discrete, named action.

## Owned State

A session service owns:

- session identity, version, and lifecycle state
- the ordered log of session records
- turn grouping (the service infers turn boundaries from the record stream)
- session metadata and usage accounting
- record identity, sequence, and timestamps

The service may store this state however it wants. The contract does not
require a database, event log, flat file, or branch tree.

## Session Object

A session has a stable identity and a monotonic version.

```text
Session {
    id
    version
    state
    created_at
    updated_at
    deleted_at?
    metadata: SessionMetadata
    usage:    SessionUsage
    records:  SessionRecord[]
}
```

`id` is stable for the life of the session.

`version` starts at `1` on creation and increases when an accepted write
changes session state. Reads must not change the version.

`state` is the lifecycle state visible to the caller. Base states:

```text
active
deleted
```

An implementation may have richer internal states (archived, branched,
compacted, expired, locked). Those are implementation philosophy unless
advertised as part of a richer surface.

`created_at`, `updated_at`, and `deleted_at` are service-owned timestamps.
`deleted_at` is present only after deletion.

## Session Metadata

Optional, surface-oriented information the session service preserves for
resume, display, routing, and diagnostics.

```text
SessionMetadata {
    title?:          string
    display_name?:   string
    cwd?:            string
    model_provider?: string
    model?:          string
    custom?:         map<string, json>
}
```

`cwd` records the working directory associated with the session. Local CLI
sessions often have one; gateway, cron, browser, or eval sessions may not. A
missing `cwd` must not make a session invalid.

`model_provider` and `model` record the model identity when known. They are
metadata for inspection and diagnostics; they do not make the session service a
model adapter.

`custom` is an implementation-extension map. The service preserves it but does
not interpret it.

`set_metadata` replaces the entire metadata object. It is not a field-level
merge unless a richer service advertises merge semantics.

## Usage Surface

The session preserves runtime-visible accounting for diagnostics, continuation
decisions, context pressure, and user surfaces.

```text
SessionUsage {
    char_count
    last_prompt_tokens:        TokenCount
    last_output_tokens:        TokenCount
    total_input_tokens:        TokenCount
    total_output_tokens:       TokenCount
    total_reasoning_tokens:    TokenCount
    cache_read_tokens:         TokenCount
    cache_write_tokens:        TokenCount
    context_window_tokens:     TokenCount
    last_context_used_pct
    api_call_count
}

TokenCount {
    value
    source
}

TokenCountSource = char_estimate | tokenizer | provider
```

Each token-valued field uses `TokenCount` so the session records both the value
and whether it came from a character estimate, a tokenizer, or a provider.
Unknown values may be zero. `last_context_used_pct` records the most recent
known fraction of the model context window used.

`set_usage` merges the provided fields into the current usage object. Fields present in the input overwrite the current values; absent fields are left untouched. The kernel owns session usage — it is the sole writer. The store does not auto-update usage on record writes.

## Session Record

A session record is the base unit of the conversation log.

```text
SessionRecord {
    id          — required, stable record identity
    turn_id     — assigned by the session service, groups records into turns
    kind        — required: message | tool_call | tool_result | system_note
    role        — required when kind=message: user | assistant | system
    text        — canonical content, required for message and tool_result records
    refs        — optional pre-resolved context references
    tool_calls  — required for tool_call records: list of ToolCall
    call_id     — required for tool_result records, links to the originating tool_call
    created_at  — assigned by the session service
}
```

`id` is a stable record identity assigned by the service. It is independent of
array position.

`turn_id` groups records into turns. The session service infers turn boundaries
from the record stream — a user message opens a new turn, and all subsequent
records belong to that turn until the next user message. The caller does not
provide turn_id; the service owns the grouping algorithm.

`kind` identifies the record category. The base kinds are `message`,
`tool_call`, `tool_result`, and `system_note`.

`role` is required when `kind` is `message` and must be one of `user`,
`assistant`, or `system`. It must be absent for other record kinds.

`text` is the canonical record content. For a message record, it is the
conversational text. For a tool_result record, it is the tool's output. For a
tool_call record, it is absent — the structured `tool_calls` field carries the
invocation.

`refs` are pre-resolved context references. A ref is a pointer from a record to
external material — a file, URL, artifact, or other session record. The session
service stores refs; it does not resolve, fetch, or validate them. Callers set
refs before writing the record.

```text
ContextRef {
    kind      — required: file | directory | url | artifact | memory | session_record
    target    — required, identity of the referenced material
    label?    — optional human-readable label
    range?    — optional sub-range within the target
    metadata? — optional extension map
}
```

`tool_calls` carries the structured tool invocation for `kind=tool_call`
records. Each entry names the tool, its arguments, and the call identity.

```text
ToolCall {
    id        — call identity (provider-assigned or service-assigned)
    name      — tool name
    arguments — tool arguments as key-value pairs
    tool_id?  — optional registered tool identifier
}
```

`call_id` on a `tool_result` record links back to the `ToolCall.id` of the
originating `tool_call` record.

`created_at` records when the record was accepted by the service.

## Turn Grouping

The session service owns turn grouping. The base algorithm: a `write_message`
with `role=user` opens a new turn. Every subsequent record — assistant
messages, tool calls, tool results, system notes — inherits that turn until the
next user message.

This is an implementation detail, not a contract action. The observable
guarantee is that records carry a `turn_id` assigned by the service, and the
assignment is consistent with the algorithm above.

Autonomous turns (loops without user messages) are out of scope for v0.

## Actions

Every response carries `{id, version, state}`. Action-specific fields are
additional.

### `session.create`

Start a new session from an initial user prompt.

Input:

```text
{
    prompt:   string           — required, non-empty after trimming
    metadata?: SessionMetadata — optional session metadata
    refs?:    ContextRef[]     — optional refs for the initial user message
}
```

The prompt becomes the first ordered record: `kind=message`, `role=user`,
`text=prompt`. The service assigns `id`, `turn_id`, and `created_at`.

Returns: `{id, version: 1, state: "active"}`

Terminal events: `session.created` | `session.create_rejected`

### `session.get`

Return the current session by ID.

Input: `{id: string}`

Returns: `{id, version, state, metadata, usage, records}`

Read-only. Must not mutate session state. Records are returned in canonical
order. Deleted sessions are rejected.

Terminal events: `session.retrieved` | `session.retrieve_rejected`

### `session.delete`

Delete a session when policy allows it.

Input: `{id: string}`

Soft delete: sets `state=deleted`, sets `deleted_at`, advances version. An
already-deleted session returns the current state without another version bump.
After deletion, `get` and all write actions reject.

Returns: `{id, version, state: "deleted"}`

Terminal events: `session.deleted` | `session.delete_rejected`

### `session.write_message`

Write a conversational message to the session.

Input:

```text
{
    session_id: string        — target session
    text:       string        — required, non-empty message content
    role:       string        — required: user | assistant | system
    refs?:      ContextRef[]  — optional context references
}
```

The service constructs a record with `kind=message` and the provided `text`,
`role`, and `refs`. It assigns `id`, `turn_id`, and `created_at`. A user
message opens a new turn; an assistant or system message extends the current
turn.

Advances version. Rejects deleted sessions.

Returns: `{id, record_id, version, state}`

Terminal events: `session.message_written` | `session.message_write_rejected`

### `session.write_tool_call`

Write a tool invocation record.

Input:

```text
{
    session_id: string       — target session
    name:       string       — required, tool name
    arguments:  map          — required, tool arguments
    call_id:    string       — required, provider-assigned call identity
    tool_id?:   string       — optional registered tool identifier
    refs?:      ContextRef[] — optional context references
}
```

The service constructs a record with `kind=tool_call` and the provided
invocation fields. It assigns `id`, `turn_id`, and `created_at`. Extends the
current turn.

Advances version. Rejects deleted sessions.

Returns: `{id, record_id, version, state}`

Terminal events: `session.tool_call_written` | `session.tool_call_write_rejected`

### `session.write_tool_result`

Write a tool execution result.

Input:

```text
{
    session_id: string       — target session
    text:       string       — required, tool output
    call_id:    string       — required, links to the originating ToolCall.id
    refs?:      ContextRef[] — optional context references (e.g. created files)
}
```

The service constructs a record with `kind=tool_result` and the provided `text`
and `call_id`. It assigns `id`, `turn_id`, and `created_at`. Extends the
current turn.

Advances version. Rejects deleted sessions.

Returns: `{id, record_id, version, state}`

Terminal events: `session.tool_result_written` | `session.tool_result_write_rejected`

### `session.write_system_note`

Write a session-visible annotation without a model-facing role.

Input:

```text
{
    session_id: string       — target session
    text:       string       — required, note content
    refs?:      ContextRef[] — optional context references
}
```

The service constructs a record with `kind=system_note`. It assigns `id`,
`turn_id`, and `created_at`. Extends the current turn.

Advances version. Rejects deleted sessions.

Returns: `{id, record_id, version, state}`

Terminal events: `session.system_note_written` | `session.system_note_write_rejected`

### `session.write_record`

Write an arbitrary record with full caller control over the record shape. This
is the generic escape hatch for record kinds not covered by the dedicated
write actions and for callers that already have a complete record shape
(compaction, replay, import).

Input:

```text
{
    session_id: string        — target session
    record:     SessionRecord — required, caller-provided record
}
```

The service assigns `id`, `turn_id`, and `created_at` if the caller does not
provide them. The service may reject records with unknown `kind` values or
invalid field combinations.

Advances version. Rejects deleted sessions.

Returns: `{id, record_id, version, state}`

Terminal events: `session.record_written` | `session.record_write_rejected`

### `session.set_metadata`

Replace the session metadata object.

Input:

```text
{
    session_id: string          — target session
    metadata:   SessionMetadata — required, replacement metadata
}
```

Full replacement, not a merge. Advances version. Rejects deleted sessions.

Returns: `{id, version, state}`

Terminal events: `session.metadata_set` | `session.metadata_set_rejected`

### `session.set_usage`

Update the session usage object.

Input:

```text
{
    session_id: string       — target session
    usage:      SessionUsage — usage fields to merge
}
```

Merges provided fields into the current usage object. Fields present in the
input overwrite current values; absent fields are left untouched. This lets
callers supply provider-verified token counts without needing to carry every
auto-computed field. Advances version. Rejects deleted sessions.

Returns: `{id, version, state}`

Terminal events: `session.usage_set` | `session.usage_set_rejected`

## Unsupported Requests

If a caller asks for an action the service does not support, the outcome should
be recorded as an explicit unsupported result, not as an unknown exception.

```text
command: session.branch
terminal event: capability.unsupported
```

## Invariants

- A created session starts active with version `1` and exactly one user message
  record.
- A session has a stable `id` until deletion or until the service explicitly
  reports otherwise.
- Session versions are monotonic for accepted state changes.
- `get` must not mutate session state.
- `get` returns records in canonical session order.
- Every write advances the session version and updates `updated_at`.
- A rejected write must not change session state.
- `write_message` with `role=user` opens a new turn for subsequent records.
- `turn_id` is assigned by the session service; the caller does not provide it.
- Accepted records preserve supplied `refs` unless the service explicitly
  rejects or redacts them.
- Missing `cwd` metadata is valid.
- Deleted sessions are not retrievable or writable in the base contract.
- `set_metadata` is a full replacement, not a merge.
- `set_usage` is a merge: provided fields overwrite, absent fields are preserved.

## Failure Semantics

Expected failure categories:

- invalid session input (missing or empty required fields)
- missing create prompt
- session not found
- session deleted
- invalid record (unknown kind, missing required fields for kind)
- persistence unavailable
- delete denied
- action unsupported

Failures are typed enough for the runtime or surface to decide whether to
retry, degrade, ask the user, or stop.

## Compaction Interaction

Compaction is not part of the session capability.

The compaction service owns the transform strategy. The session service owns the
record of whatever state is accepted back into the session through
`write_record` or a richer advertised surface.

## Memory Interaction

Memory services should not need to know the concrete session implementation.

The runtime or mediator should pass session observations deliberately:

- session created
- records written
- session deleted

If a memory service wants full transcript access, it should request
`session.get` through the mediator.

## Lifecycle

```text
created -> active -> mutated many times -> deleted
```

Deletion is terminal for `get` and all write actions in the base surface.
Richer services may offer restore, branch, fork, archive, or expiration
semantics by advertising additional actions and states.

## Minimal Test Fixtures

A service implementing the base session contract should be testable with:

- advertise `session.v0.3`
- create a session from a prompt and receive a stable `id`, version `1`, and
  `state: "active"`
- reject an empty or whitespace-only creation prompt
- `get` the session with one user message record, ordered
- `get` rejects a deleted session
- `get` rejects a missing session
- `write_message` with role `user` opens a new turn
- `write_message` with role `assistant` extends the current turn
- consecutive `write_message` calls produce consecutive turn_ids for user
  messages and consistent turn_ids for assistant messages within a turn
- `write_tool_call` and `write_tool_result` extend the current turn
- `write_tool_result` with a `call_id` that matches a prior `ToolCall.id`
- `write_system_note` extends the current turn
- `write_record` accepts valid records of known kinds
- `write_message` with `refs` preserves them on `get`
- `set_metadata` replaces the metadata object and is reflected on `get`
- `set_usage` replaces the usage object and is reflected on `get`
- version advances on every accepted write
- version does not advance on `get`
- delete an active session and verify `state: "deleted"` with version advanced
- delete an already-deleted session without a second version bump
- reject writes on a deleted session
- reject `get` on a deleted session
- reject an unsupported action such as `session.branch`
