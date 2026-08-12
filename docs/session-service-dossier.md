# Session Service Dossier

Date: 2026-08-11

Contract version: `session.v0.3`.

Status: draft.

Contract draft: `docs/session-capability-contract.md`.

This is a concept-stage dossier, not a formal contract or API specification.
The purpose is to describe the session capability boundary that a decent
agentic harness should be able to depend on, while leaving implementation
philosophy open.

Frankenstein should not prescribe whether a session implementation is a flat
transcript, append-only log, provider-native thread, branchable tree, or
something else. It should define the minimum outside-visible promise that lets
the rest of the harness run against interchangeable session services.

## Dossier

Capability:
Session service / session experience.

User-visible job:
Let users create, continue, inspect, and delete agent sessions while
maintaining trust that the harness is continuing from the intended prior state.
If the session service exposes model-facing tools, it should advertise them
explicitly.

Runtime job:
Own the active session identity and the session state needed for continuation.
Accept record writes (messages, tool calls, tool results, system notes),
metadata updates, and usage updates. Expose the full ordered transcript on
`get`. Expose lifecycle metadata needed by surfaces.

State owned or mutated:
Session identity, lifecycle status, ordered conversation records, turn
grouping, optional session metadata such as `title`, `display_name`, `cwd`,
`model_provider`, `model`, and `custom`, and any model-facing tool metadata the
session service chooses to publish.

Inputs:
Session lifecycle commands (`create`, `get`, `delete`), record writes
(`write_message`, `write_tool_call`, `write_tool_result`,
`write_system_note`, `write_record`), metadata replacement
(`set_metadata`), usage replacement (`set_usage`).

Outputs:
Created session identity (`id`, `version`, `state`), retrieved session state
(full object with records, metadata, usage), and clear failure results when a
session operation cannot be performed. Write responses return `{id, record_id,
version, state}` — the session identity, the new record's identity, and the
updated version and state. Metadata and usage set responses return `{id,
version, state}`.

Side effects:
Persist or update session state in the implementation's chosen storage and
update any indexes, caches, or surface-visible metadata owned by that
implementation.

Failure modes:
Session cannot be created, requested session does not exist or is deleted,
record cannot be written (invalid kind, missing required fields, persistence
unavailable), metadata or usage cannot be replaced, deletion is denied or
unsupported, or an action is unsupported.

Recovery behavior:
The service should fail explicitly rather than pretend a write succeeded.
Writes should leave the active session in a coherent state or return a failure
that the runtime can surface or recover from.

Hidden coupling:
Session state is the bridge between turns. It affects runtime continuation,
context construction, memory observations, UI/gateway presentation,
observability, and long-session behavior. The boundary should be small, but it
cannot be treated as passive storage.

Record surface:
The base session record exposes:

- `id` — stable record identity assigned by the service
- `turn_id` — assigned by the session service, groups records into turns. The
  service infers turn boundaries: a user message opens a new turn; subsequent
  records extend it. The caller never provides `turn_id`.
- `kind` — `message`, `tool_call`, `tool_result`, or `system_note`
- `role` — `user`, `assistant`, or `system` (required for `kind=message`)
- `text` — canonical content (required for message and tool_result)
- `refs` — pre-resolved pointers to files, URLs, artifacts, or other session
  records. The session service stores them; it does not resolve or validate
  them.
- `tool_calls` — structured invocation for `kind=tool_call`
- `call_id` — links a `tool_result` back to its originating `tool_call`
- `created_at` — assigned by the service

Fields that are implementation-private (raw provider payloads, per-record char
counts, per-record token estimates, internal sequence numbers) are not part of
the base record surface because no external consumer has an observed need for
them.

Session metadata may include optional labels, `cwd`, model identity, and custom
extension data. `cwd` should not be mandatory because gateway, remote-backend,
cron, browser, imported, and eval sessions may not have one.

The base record surface should not add `touched_paths` yet. Tool-derived path
evidence is useful, especially inside a still-running agentic loop, but it can
remain runtime-local or context-provider request metadata until implementation
experience shows that it deserves canonical session persistence. Explicit
user-facing path references should flow through `refs`.

Possible alternate philosophies:
Ephemeral sessions, flat append-only transcripts, immutable event logs,
provider-native threads, searchable episodic stores, branchable trees,
collaborative sessions, privacy-minimized sessions, and eval-only replay
sessions.

Contract-worthy? yes.

Reason:
Swapping the session service changes the kind of harness being built. Session
continuity, transcript access, deletion, persistence, and tool exposure are
meaningful user-visible and runtime-visible choices. A small session contract lets
different implementations be evaluated end to end without rewriting the rest of
the harness.

## Minimum Surface

The base session surface:

- create a new session from an initial user prompt
- reject an empty session creation prompt
- get the session by ID (full object: records, metadata, usage)
- write a message (user, assistant, or system) — the daily driver
- write a tool call record
- write a tool result record
- write a system note (session-visible, no model-facing role)
- write a generic record (escape hatch for compaction, replay, future kinds)
- set session metadata (full replacement)
- set session usage (full replacement)
- delete a session when policy allows it
- advertise model-facing session tools, if any

Everything beyond the minimum surface is implementation philosophy. A service
may support search, branching, forking, rewind, transcript windows,
collaborative state, import/export, or durable replay, but those are not needed
to explain the base idea.

## Decisions Recorded

These decisions shaped the v0.3 surface. Source: collaborator session
2026-08-11, starting from kernel runtime implementation friction.

- **Drop `mutate` in favor of dedicated write actions.** The mutation envelope
  (`MutationOp` → `MutateInput` → `Mutate`) forced every caller to construct
  three nested types to append a record. Dedicated actions (`write_message`,
  `write_tool_call`, `write_tool_result`, `write_system_note`,
  `write_record`, `set_metadata`, `set_usage`) eliminate the dispatch
  boilerplate. `write_record` is the generic escape hatch for batch operations
  and future record kinds.

- **Merge `resume` and `read` into `get`.** Both were read-only operations
  that returned session state. The distinction ("I'm about to write" vs. "I
  just want to look") was caller intent with no service-side behavior
  difference. A single `get` action is cleaner.

- **Drop `materialize`.** It was identical to `get` in the base contract
  (both returned `ordered_records`). The `ContinuationKind` discriminator was
  forward-compatibility with no current semantic weight. If a richer service
  needs compacted or provider-native continuation, it advertises additional
  actions on its own version.

- **Turn grouping owned by the session service.** The kernel no longer
  generates or passes `turn_id`. The session service infers turn boundaries
  from the record stream: a user message opens a new turn, subsequent records
  extend it. Autonomous turns (no user messages) are out of scope for v0.

- **State is a first-class field on every response.** Every action returns
  `{id, version, state}` as the base tuple. Write responses add `record_id`.
  Get returns the full session object.

- **Write responses are minimal.** Write actions return `{id, record_id,
  version, state}` — not the full session. If the caller needs the updated
  session, it calls `get`. This keeps writes cheap and avoids payload bloat
  when the store is a simple KV.

- **Per-record char_count and tokens removed from the base record surface.**
  Session-level `SessionUsage` tracks accounting. Per-record values are
  implementation-private until an external consumer needs them.

- **`raw` (provider-native payload) removed from the base record surface.**
  The contract already stated other capabilities cannot assume it is present.
  Services may store it privately.

- **`set_usage` uses merge semantics.** The store auto-computes record-derived
  fields (char_count, last_prompt_tokens) on every record write. The kernel
  supplies provider-verified token counts via `set_usage`, which merges
  provided fields into the current usage object — overwrite what was provided,
  preserve what was absent. Split ownership by field, no coordination needed.
  The ideal future direction is auto-update (record writes automatically
  update session-level usage), but the explicit merge is more flexible for an
  evolving harness.

## Open Questions

- **Idempotency.** The v0.2 `mutate` action had idempotency key support.
  None of the current callers use it. If it becomes necessary, it should be an
  optional field on each write action rather than a reason to reintroduce
  `mutate`.

- **Batch writes.** The v0.2 `mutate` supported multi-op batches in a single
  transaction. v0.3 writes are individual actions. If atomic multi-record
  batches become necessary, add a dedicated `write_records` (plural) action
  rather than reintroducing the mutation envelope.

## Compaction Boundary

Compaction should be its own capability. It is a long-session transform, not a
session lifecycle primitive.

The session service should expose the transcript through `get`. The compaction
service should decide how to transform that state. The runtime or mediator
should then apply the accepted compaction result back to the session through
`write_record`.

This keeps the session service focused on continuity and transcript truth, while
leaving compaction strategy independently swappable.

## Episodic Memory Ownership

The canonical transcript should belong to the session service, not the memory
service. Memory services may observe, index, summarize, extract, or mirror
episodes, but those are projections unless the selected service explicitly
implements both session and memory capabilities.

The split should stay simple:

- Session owns continuity and transcript truth.
- Memory owns distilled facts, profiles, semantic recall, and memory tools.
- The runtime or mediator passes session observations to memory deliberately.
