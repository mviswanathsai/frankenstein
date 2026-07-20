# Session Capability Contract

Date: 2026-07-18

Contract version: `session.v0.2`.

Status: draft.

This is a lightweight contract sketch, not a wire protocol or OpenAPI-style
schema. It names the minimum outside-visible promises a session service must
make so the rest of an agentic harness can use it without knowing its internals.

The initial version was verified against the `internal/session` implementation.
This draft is now being refined as adjacent contracts exercise it, so the Go
reference service may temporarily lag accepted contract changes. The document
is the capability shape the rest of the harness should be able to rely on.

The contract should stay small. A service may be much richer than this, but the
runtime can only assume the base surface below unless the service advertises
more.

## Purpose

The session capability owns conversation continuity.

It gives the harness a stable place to create, resume, mutate, inspect,
materialize, and delete agent sessions. It also exposes the active continuation
state the runtime needs to keep working from the intended prior state.

Session storage and context materialization are related but not identical.
`session.read` returns the canonical ordered session record visible to the
caller. `session.materialize` returns the continuation state the runtime should
use for the next turn.

## Owned State

A session service owns:

- session identity and metadata
- session lifecycle from creation, persistence, mutation and deletion
- session logic: append-only, branching, forking, roll-back etc.,
- usage/accounting state associated with each session

The service may store this state however it wants. The contract does not require
a database, event log, flat transcript file, provider thread, or branch tree.

## Session Object

Successful `session.create`, `session.resume`, `session.mutate`,
`session.read`, and `session.delete` return a session object as their terminal
payload.

```text
Session {
  id
  version
  state
  created_at
  updated_at
  deleted_at?
  metadata: SessionMetadata
  usage: SessionUsage
  records: SessionRecord[]
}
```

`metadata` is exactly one `SessionMetadata` value. `usage` is a separate
`SessionUsage` value, not a second metadata value and not nested inside
`metadata`. The distinction is observable in `session.mutate`: `set_metadata`
replaces `SessionMetadata`, while `set_usage` replaces `SessionUsage`.

`id` is stable for the life of the session.

`version` starts at `1` on creation and increases when accepted mutations change
session state. A read or resume must not change the version. A delete that
changes an active session to deleted is a state change and should advance the
version. Repeating delete against an already-deleted session may return the
current deleted session without another state change.

`state` is the lifecycle state visible to the caller. The base lifecycle states
are:

```text
active
deleted
```

An implementation may have richer internal states such as archived, branched,
forked, compacted, expired, locked, or collaborative. Those are implementation
philosophy unless advertised as part of a richer surface.

`created_at`, `updated_at`, and `deleted_at` are service-owned timestamps.
`deleted_at` is present only after deletion.

## Session Metadata

The base metadata surface is optional and surface-oriented. It is not context
by itself; it is information the session service stores for resume, display,
routing, and diagnostics.

```text
SessionMetadata {
  title?: string
  display_name?: string
  cwd?: string
  model_provider?: string
  model?: string
  custom?: map<string, json>
}
```

`title` and `display_name` are user/surface-facing labels. They are optional.

`cwd` records the working directory associated with the session when the runtime
or surface has a meaningful one. Local CLI sessions often do; gateway, cron,
browser, remote-backend, imported, or eval sessions may not. A missing `cwd`
must not make a session invalid.

`model_provider` and `model` record the model identity associated with the
session when known. They are metadata for inspection, resume, and diagnostics;
they do not make the session service a model adapter.

`custom` is an implementation-extension map for metadata that should be
preserved by the session service but is not part of the base shared vocabulary.

The base mutation model treats `set_metadata` as replacement of the metadata
object supplied by the caller, not a field-level merge, unless a richer service
surface advertises merge semantics.

## Usage Surface

The session capability preserves usage state because session continuity includes
the runtime-visible accounting needed for diagnostics, continuation decisions,
context pressure, and user surfaces.

```text
SessionUsage {
  char_count
  last_prompt_tokens: TokenCount
  last_output_tokens: TokenCount
  total_input_tokens: TokenCount
  total_output_tokens: TokenCount
  total_reasoning_tokens: TokenCount
  cache_read_tokens: TokenCount
  cache_write_tokens: TokenCount
  context_window_tokens: TokenCount
  last_context_used_pct
  api_call_count
}

TokenCount {
  value
  source
}

TokenCountSource = char_estimate | tokenizer | provider
```

`char_count` is the total stored textual size available for cheap diagnostics
and rough pressure checks. Each token-valued field uses `TokenCount` so the
session records both the value and whether it came from a character estimate, a
tokenizer, or a provider. `last_context_used_pct` records the most recent known
fraction of the model context window used. `api_call_count` records the number
of model API calls accounted to the session.

Unknown numeric usage values may be zero. Token counts identify their source so
a runtime or surface can distinguish rough estimates from tokenizer- or
provider-supplied values. Token accounting belongs to this session-level usage
surface; it is not required on every conversation record.

Appending records may update usage best-effort. A model adapter or runtime may
later replace the usage object through `session.mutate` with provider-verified
usage.

## Session Record Surface

The base contract does not prescribe an internal transcript format, but ordered
records returned by `session.read` or embedded in a materialized continuation
must expose these top-level fields:

```text
SessionRecord {
  id
  refs[]
  kind
  role?
  text
  created_at
  updated_at
}
```

`id` is required because records need a stable identity independent of array
position. References, provenance, diagnostics, and richer advertised mutation
surfaces may use it to address one particular record. The service may assign it
when the caller does not provide one and may reject a caller-supplied ID that
would collide within the session.

Array position expresses canonical record order in the base contract.
Implementations may maintain internal sequence numbers, but the base record
does not expose one because it has no sequence-addressed read, pagination, or
range operation.

`refs` is required, though it may be empty, so callers always receive one
unambiguous reference collection. Its values are pre-resolved references
associated with the record. They may point to files, URLs, artifacts, memory
records, prior messages, or other externally addressable material.

Sketch:

```text
ContextRef {
  kind
  target
}
```

`kind` is required so every service interprets a target consistently rather
than independently guessing from its string form. Base descriptive kinds are:

```text
file
directory
url
artifact
memory
session_record
```

Unknown namespaced kinds may be preserved. `target` is the required address or
identity of the referenced material. Labels, ranges, and arbitrary metadata are
not part of the base reference shape until a concrete cross-capability consumer
requires them.

The session service stores and returns refs; it does not have to parse raw text,
read files, fetch URLs, or decide how refs become model context. Frontends,
gateways, runtimes, or specialized providers may create refs before passing
records to the session service.

`kind` identifies the record category. The base record kinds are:

```text
message
tool_call
tool_result
system_note
```

If omitted in an append operation, a service may default `kind` to `message`.
It is required on returned records because role alone cannot distinguish a
message from a tool call, tool result, or system note.

`role` is the conversation role when the record represents a model-facing or
user-facing message. Creation from a user prompt must create an initial
`message` record with role `user`. It is optional because non-message record
kinds do not necessarily have a conversation role.

`text` is required canonical conversation content. It is normalized text, not
provider-formatted replay data. Provider-native model payloads belong to the
model-adapter or runtime-event surfaces, and structured tool payloads belong to
the tool invocation and execution surfaces. A session service may persist those
privately, but other capabilities cannot assume they are present in a session
record.

`created_at` records when the record was accepted. `updated_at` initially equals
`created_at` and records the most recent accepted edit when a richer session
service advertises editable records. The base actions do not currently expose a
record-edit operation, but the record shape allows such an implementation
without inventing a second record type.

The base record surface intentionally does not include a first-class
`touched_paths` field. Tool-derived path evidence can remain runtime-local or
context-provider request metadata until real implementations show that it needs
canonical session persistence. If a path is an explicit user-facing reference,
it should normally be represented through `refs`.

## Required Actions

### `session.create`

Start a new session from an initial user prompt.

Base input payload:

```json
{
  "prompt": "user prompt text",
  "refs": [],
  "metadata": {
    "title": "optional title",
    "display_name": "optional display label",
    "cwd": "optional working directory",
    "model_provider": "optional provider id",
    "model": "optional model id",
    "custom": {}
  }
}
```

The prompt is required after trimming whitespace. The base runtime should not
create an empty interactive session.

The accepted prompt becomes the first ordered user message in the session.
Creation returns a session with:

- stable session `id`
- `version` of `1`
- `state` of `active`
- `created_at` and `updated_at`
- supplied metadata
- initial usage/accounting state
- exactly one initial record with `kind` of `message`, `role` of `user`, and
  `text` equal to the prompt

Supplied `refs` annotate that first user record when present.

Terminal events:

- `session.created`
- `session.create_rejected`

### `session.resume`

Attach the current runtime to an existing active session.

Base input payload:

```json
{
  "id": "session id"
}
```

This is the action used when a CLI resumes a session, a UI opens a prior
conversation, or a gateway resolves an incoming message to an existing session.

Resume returns the current session object. It must not mutate the session,
change `updated_at`, or create accidental continuity when the session is
missing.

Deleted sessions are not resumable in the base contract.

Terminal events:

- `session.resumed`
- `session.resume_rejected`

### `session.mutate`

Apply a coherent mutation to an active session.

Base input payload:

```json
{
  "id": "session id",
  "idempotency_key": "optional duplicate-protection key",
  "ops": [
    {
      "type": "append_record",
      "record": {}
    },
    {
      "type": "set_metadata",
      "metadata": {}
    },
    {
      "type": "set_usage",
      "usage": {}
    }
  ]
}
```

`id` is required. `ops` must be non-empty.

The base mutation operations are:

- `append_record`: append one record to the ordered session record.
- `set_metadata`: replace the session metadata object.
- `set_usage`: replace the session usage object.

Each operation must include the payload required by its type. Unknown operation
types are invalid mutations unless a richer advertised surface adds them.

The service must either apply the mutation coherently or reject it explicitly.
For a non-idempotent accepted mutation, the service advances the session
version once for the whole mutation, updates `updated_at`, and returns the
updated session object.

For appended records, the service assigns any missing base fields it owns, such
as `id`, `kind`, `created_at`, and `updated_at`. Accepted record mutations
preserve supplied top-level `refs` unless the service explicitly rejects or
redacts them.

If `idempotency_key` is supplied, the service must prevent the same mutation
from being applied twice to the same session. A repeat request may return the
current session state rather than replaying the exact historical direct-call
return, unless the service advertises stronger event replay semantics.

The base mutation payload does not include an expected-version guard. Stronger
compare-and-swap behavior can be advertised by a richer service surface if a
harness actually uses it.

Deleted sessions cannot be mutated in the base contract.

Terminal events:

- `session.mutated`
- `session.mutation_rejected`

### `session.read`

Return the canonical ordered session record visible to the caller.

Base input payload:

```json
{
  "id": "session id"
}
```

This is the canonical user-facing/session-facing history read. Implementations
may redact or filter for policy reasons, but redaction must be explicit in the
result.

Read returns a session object and must not mutate session state. Records must be
returned in canonical session order.

Deleted sessions are not readable in the base contract unless a richer service
surface advertises tombstone or audit reads explicitly.

Terminal events:

- `session.read_completed`
- `session.read_rejected`

### `session.materialize`

Return the current continuation state needed by the runtime.

Base input payload:

```json
{
  "id": "session id"
}
```

Base terminal payload:

```text
MaterializedSession {
  session_id
  version
  state
  kind
  metadata: SessionMetadata
  usage: SessionUsage
  records: SessionRecord[]
}

ContinuationKind = ordered_records
```

For `session.v0.2`, a base service must support `ordered_records`
materialization. Richer services may advertise additional continuation kinds,
such as a compacted state or provider-native thread reference, but the `kind`
field must make that explicit so the runtime does not guess.

Materialization returns continuation state derived from a known active session
version. It must not mutate session state.

Deleted sessions cannot be materialized in the base contract.

Terminal events:

- `session.materialized`
- `session.materialization_rejected`

### `session.delete`

Delete a session when policy allows it.

Base input payload:

```json
{
  "id": "session id"
}
```

Deletion may be unsupported, denied, soft, or hard depending on the
implementation and deployment policy. The result must say what happened.

For the base soft-delete behavior, an accepted delete changes an active session
to `deleted`, sets `deleted_at`, advances the session version, updates
`updated_at`, and returns the deleted session object. After deletion, resume,
read, materialize, and mutate reject the session as deleted.

Deleting an already-deleted session may return the current deleted session
without another state change.

Terminal events:

- `session.deleted`
- `session.delete_rejected`

## Unsupported Requests

If a caller asks for an action the service does not support, the outcome should
be recorded as an explicit unsupported result, not as an unknown exception.

Example:

```text
command: session.branch
terminal event: capability.unsupported
```

This lets UIs, model-facing tools, memory, compaction, and experiments request
richer behavior without hardcoding service-specific APIs.

## Invariants

- A created or resumed session has a stable session identity until deletion or
  until the service explicitly reports otherwise.
- Creation from a prompt creates exactly one initial user message record.
- A created session starts active with version `1`.
- Session versions are monotonic for accepted state changes.
- Resume, read, and materialize must not mutate session state.
- Mutations are applied atomically and ordered by the session service.
- A successful non-idempotent mutation has a new observable session version.
- A rejected mutation must not pretend to have changed session state.
- Repeating an idempotent mutation must not append duplicate records or apply
  duplicate state changes.
- `session.read` returns the service's canonical ordered record for the caller.
- `session.materialize` returns continuation state derived from a known session
  version.
- Accepted record mutations preserve supplied top-level `refs` unless the
  service explicitly rejects or redacts them.
- Missing `cwd` metadata is valid.
- Failed resume must not silently create accidental continuity.
- Deleted sessions are not resumable, readable, materializable, or mutable in
  the base contract.
- Failed compaction application, via `session.mutate`, must not silently drop
  usable session state.
- Model-facing tools published by the session service are part of its advertised
  surface and should route back through the mediator.

## Failure Semantics

Expected failure categories:

- invalid session input
- missing create prompt
- invalid or missing session reference
- session not found
- session deleted
- invalid mutation
- persistence unavailable
- read unavailable
- materialization unavailable
- delete denied
- action unsupported
- policy denied

The exact error payload can evolve. The required behavior is that failures are
typed enough for the runtime or surface to decide whether to retry, degrade,
ask the user, or stop.

For the current reference behavior:

- empty or whitespace-only create prompts fail as invalid input
- missing IDs fail as invalid input
- missing sessions fail as not found
- deleted sessions fail as deleted for resume, read, materialize, and mutate
- empty mutation op lists fail as invalid mutation
- mutation ops without their required payload fail as invalid mutation
- unknown mutation op types fail as invalid mutation

## Compaction Interaction

Compaction is not part of the session capability.

The compaction service owns the transform strategy. The session service owns the
record of whatever continuation state is accepted back into the session.

## Memory Interaction

Memory services should not need to know the concrete session implementation.

The runtime or mediator should pass session observations deliberately:

- session created
- turn/session mutated
- transcript or ordered record read, when allowed
- session deleted

If a memory service wants full transcript access, it should request
`session.read` through the mediator. If the session service rejects or does not
support the needed read shape, memory can degrade or fail according to its own
contract.

## Lifecycle

The base lifecycle is intentionally simple:

```text
created -> active/resumable -> mutated many times -> deleted
```

Deletion is terminal for resume/read/materialize/mutate in the base surface.
Richer services may offer restore, audit read, branch, fork, archive, or
expiration semantics by advertising additional actions and states.

## Minimal Test Fixtures

A service implementing the base session contract should be testable with:

- advertise `session.v0.2`
- create a session from a user prompt and receive a stable session identity,
  version `1`, active state, and one initial user message record
- reject an empty or whitespace-only creation prompt
- create or mutate with optional metadata fields such as `title`,
  `display_name`, `cwd`, `model_provider`, `model`, and `custom`
- mutate with a record that has top-level `refs`
- read the ordered record back with supplied `refs` preserved
- return read records in canonical session order
- materialize continuation state with kind `ordered_records`
- resume the session by reference without mutating it
- reject resume, read, and materialize for a missing session
- reject invalid mutations, including missing ops and missing required op
  payloads
- append a record and update session usage accounting with an identified token
  source
- replace usage with provider/tokenizer-supplied accounting through
  `set_usage`
- avoid double-applying the same idempotent mutation
- delete a session and make it unresumable, unreadable, unmaterializable, and
  immutable through the base surface
- reject an unsupported richer action such as `session.branch`
