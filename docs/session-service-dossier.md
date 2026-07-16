# Session Service Dossier

Date: 2026-07-15

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
Let users create, resume, continue, inspect, and delete agent sessions while
maintaining trust that the harness is continuing from the intended prior state.
If the session service exposes model-facing tools, it should advertise them
explicitly.

Runtime job:
Own the active session identity and the session state needed for continuation.
Accept session lifecycle mutations, expose the full ordered transcript,
materialize the continuation state needed by the runtime, and expose lifecycle
metadata needed by surfaces.

State owned or mutated:
Session identity, lifecycle status, active continuation state, ordered
conversation records or events, session metadata for surfaces, and any
model-facing tool metadata the session service chooses to publish.

Inputs:
Session lifecycle commands, the initial user prompt for session creation,
existing session references, user/assistant/tool turn records, metadata updates,
transcript read requests, deletion requests, and implementation-specific data
needed to materialize continuation state.

Outputs:
Created or resumed session identity, updated session state, materialized active
continuation state, full transcript or ordered record, session metadata,
advertised tool schemas if any, and clear failure results when a session
operation cannot be performed.

Side effects:
Persist or update session state in the implementation's chosen storage and
update any indexes, caches, or surface-visible metadata owned by that
implementation.

Failure modes:
Session cannot be created or resumed, the creation prompt is missing, requested
session does not exist, mutation cannot be applied coherently, continuation
state cannot be materialized, transcript cannot be read, deletion is denied or
unsupported, advertised tools are unavailable, or concurrent access would make
session state ambiguous.

Recovery behavior:
The service should fail explicitly rather than pretend a session mutation
succeeded. Failed resume must not create accidental new continuity. Mutations
should leave the active session in a coherent state or return a failure that the
runtime can surface or recover from.

Hidden coupling:
Session state is the bridge between turns. It affects runtime continuation,
context construction, memory observations, UI/gateway presentation,
observability, and long-session behavior. The boundary should be small, but it
cannot be treated as passive storage.

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

At this stage, the base session surface should stay conceptual:

- create a new session from an initial user prompt
- reject an empty session creation prompt
- resume an existing session
- mutate the active session state
- read the full transcript or ordered session record
- materialize the current continuation state needed by the runtime
- delete a session when policy allows it
- advertise model-facing session tools, if any

Everything beyond the minimum surface is implementation philosophy. A service
may support search, branching, forking, rewind, transcript windows,
collaborative state, import/export, or durable replay, but those are not needed
to explain the base idea.

## Compaction Boundary

Compaction should be its own capability. It is a long-session transform, not a
session lifecycle primitive.

The session service should expose the transcript or continuation state that a
compaction service needs to inspect. The compaction service should decide how to
transform that state. The runtime or mediator should then apply the accepted
compaction result back to the session through normal session mutation.

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
