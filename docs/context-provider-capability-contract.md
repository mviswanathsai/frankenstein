# Context Provider Capability Contract

Date: 2026-07-20

Contract version: `context_provider.v0`.

Status: draft.

This is a lightweight contract sketch, not a wire protocol or OpenAPI-style
schema. It names the minimum outside-visible promises a context provider
service must make so the rest of an agentic harness can use it without knowing
its internals.

Design rationale and Hermes evidence live in
`docs/context-provider-service-dossier.md`. This document is the capability
surface.

## Purpose

The context provider capability supplies structured context candidates for a
model input.

It gives the harness a swappable place to discover and retrieve context such as
identity, user preferences, project instructions, memory, referenced material,
skills, workspace facts, warnings, and tool guidance. It does not decide the
final model input layout.

Context provider and context builder are separate capabilities:

- the provider returns candidates with role, scope, provenance, and source
  semantics
- the builder decides what the model actually sees, where it is placed, and
  what is omitted or transformed

The context provider does not own the session's canonical conversation record,
long-term ordered storage, or final model input. It may receive a scoped view of
conversation records as request evidence, and it may return candidates derived
from that view, but storage and final input materialization remain separate
capabilities.

This contract keeps three concepts independent:

```text
action              initialize | get_context
reason              why get_context is being called now
candidate lifetime  retained | per_call
```

The action defines lifecycle semantics. The reason gives retrieval/ranking
evidence. The candidate lifetime tells the builder how long accepted output
should remain model-visible.

## Owned State

A context provider may own implementation-private state such as:

- indexes
- watches
- caches
- memory backend handles
- provider-native source IDs
- source freshness metadata
- tool schemas if it also advertises model-facing tools

The base context-provider actions are observational from the harness point of
view. Calling `context_provider.initialize` or
`context_provider.get_context` must not silently commit durable semantic state
such as new memories, profile facts, session records, or workspace changes.

If a service wants durable writes, model-facing tools, memory observation,
forget/redact operations, or index rebuild commands, those are separate
advertised surfaces.

## Candidate Lifetime

Every context response is split by model-visible lifetime:

```text
ContextBundle {
  retained[]
  per_call[]
}
```

`retained` means context that, if accepted by the builder, should continue to
be materialized in later model inputs for the active context/session lifetime
until scope exit, invalidation, replacement, or explicit removal.

`per_call` means context selected for the immediate model call. A single user
turn can contain several model calls after tool execution, so this contract uses
`per_call` instead of `per_turn`.

`retained` does not mean system prompt, filesystem-stable, cache-stable, or
durably persisted. The builder may materialize retained context as a cached
prefix, scoped overlay, model message, side channel, reference-only entry,
provider-native handle, repeated request content, or not at all.

Late-discovered retained context is allowed. For example, a subdirectory
instruction file discovered after a tool touches a path may become retained
scoped context inside the same user turn without rebuilding an already-cached
primary prefix.

## Required Actions

### `context_provider.initialize`

Establish baseline context candidates for a new context lifecycle before the
first model invocation.

This action has concrete lifecycle semantics. It is called once before the
first model invocation in a context/session lifecycle, may perform heavier
discovery, and returns candidates intended to seed the builder's retained
model-visible context. The provider may also initialize private indexes,
watches, handles, or caches needed for later retrieval.

Initialization is not represented as a `reason` for `get_context`. Keeping it
as a separate action prevents custom reasons from becoming implicit lifecycle
control signals.

Base input payload:

```text
ContextInitializeRequest {
  request_id
  session_id?

  runtime?
  workspace_roots?

  refs?
}
```

`request_id` correlates the request with the response and terminal event. If a
future mediator envelope ID fully replaces this need, a later contract version
can remove or deprecate it. In `context_provider.v0`, the response echoes it.

`session_id` is the active session identity when known.

`runtime` contains small runtime facts that are clearly known by the caller.
The base contract names only facts we have a concrete need for:

```text
RuntimeFacts {
  cwd?
  current_date?
}
```

`cwd` is the current working directory when meaningful. It may come from runtime
state, session metadata, or the active surface. `current_date` is the date the
runtime wants providers to use for time-sensitive context.

`workspace_roots` bounds file/project discovery when known. It is optional
because not every surface has a workspace.

```text
WorkspaceRoot {
  id?
  path
  label?
}
```

`refs` are pre-resolved references from the frontend, gateway, runtime, session
record, or another upstream component. The context provider should not be the
primary parser for raw user text references.

```text
ContextRef {
  kind
  target
  label?
  range?
  metadata?
}

ContextRefRange {
  unit?
  start?
  end?
}
```

`ContextRef` is intentionally aligned with the session record `refs` vocabulary
from `session.v0.1`. A context provider may dereference refs, return candidates
derived from refs, or return refs unchanged with provenance.

Terminal events:

- `context_provider.initialized`
- `context_provider.context_failed`

### `context_provider.get_context`

Supplement an established baseline with newly discovered retained context and
immediate per-call context.

Base input payload:

```text
ContextRequest {
  request_id
  session_id?
  turn_id?

  current_message?
  conversation_view?

  reason?
  runtime?

  refs?
  touched_paths?
}
```

`request_id` has the same correlation semantics as in
`context_provider.initialize`.

`session_id` is the active session identity when known.

`turn_id` is the logical turn identity when known. The session capability
preserves `turn_id` on records in `session.v0.1`, but the runtime or mediator
may be the component that generates it.

`current_message` is the role-aware current message or event that prompted the
request. It should not assume human/user origin.

Sketch:

```text
ContextMessage {
  role
  content?
  parts?
  origin?
  record_id?
  refs?
}
```

`conversation_view` has the same meaning as in
`context_provider.initialize`. It is optional and scoped; full-history access is
never implied by the presence of `session_id`.

`reason` is optional metadata that explains why the repeatable context action
was invoked. It gives the implementation retrieval and ranking context. It is
evidence, not a command, and providers are not required to branch on it.

Common reasons:

```text
user_message
tool_result
scope_change
resume
recovery
provider_policy
```

Custom reasons should preferably be namespaced, though namespacing is not
mandatory in `context_provider.v0`:

```text
company.issue_opened
obsidian.daily_note_changed
my_harness.background_followup
```

Unknown reasons should be accepted and may be treated as generic. Do not use
`context_initialization` as a reason; initialization is represented by the
separate `context_provider.initialize` action.

`runtime` has the same meaning as in `context_provider.initialize`.

`refs` has the same meaning as in `context_provider.initialize`.

`touched_paths` is optional request-time evidence from the runtime or tool
executor. It is not a canonical session-record field in this contract.

```text
TouchedPath {
  path
  source?
  operation?
  metadata?
}
```

Examples of `source` include `tool_argument`, `tool_result`, and `runtime`.
Examples of `operation` include `read`, `write`, `list`, `execute`, and
`unknown`. These values are descriptive strings in `context_provider.v0`, not a
closed enum.

Terminal events:

- `context_provider.context_provided`
- `context_provider.context_failed`

## Response Payload

Successful output payload:

```text
ContextBundle {
  request_id
  provider_id
  retained[]
  per_call[]
}
```

`request_id` echoes the request.

`provider_id` identifies the service that produced the bundle. It is required
for provenance, debugging, primary/shadow comparison, and replay.

`retained` and `per_call` contain `ContextCandidate` values. A provider may
return empty arrays.

For `context_provider.initialize`, returned candidates are normally
`retained`; `per_call` may be empty. The builder can still reject, transform, or
scope initialization candidates. For `context_provider.get_context`, either
array may contain candidates: a provider can discover new retained context
after initialization, and it can return immediate per-call context for the next
model invocation.

The base response intentionally does not include `issues`, `degradations`, or
`failures` arrays. Candidate-local concerns should use `warnings`. A terminal
provider failure should use `context_provider.context_failed`. Non-terminal
cross-candidate issue reporting can be added in a later contract version once
real implementations make it actionable.

Failure output payload:

```text
ContextFailure {
  request_id?
  provider_id?
  code
  message?
  retryable?
}
```

## Candidate Slots

The shared slot vocabulary describes context roles, not filenames or provider
internals.

Base slots:

```text
identity
user_profile
user_preferences
project_instructions
workspace_state
session_fact
memory
skills
referenced_material
file_reference
tool_guidance
capability_guidance
warning
conflict
uncertainty
custom
```

The same slot may appear in either layer:

```text
retained.memory
per_call.memory
retained.project_instructions
per_call.referenced_material
retained.skills
per_call.skills
```

Custom slots should be namespaced or provider-scoped when possible:

```text
obsidian.daily_note
company.runbook
hermes.skill_context
```

When `slot` is `custom`, `custom_slot` should identify the provider-specific
slot. A provider that relies on custom slot semantics should return enough
`rendered_text`, provenance, and metadata for a generic builder to preserve or
omit it safely.

## Candidate Shape

```text
ContextCandidate {
  id
  provider_id
  slot
  custom_slot?
  slot_description?

  content?
  rendered_text?
  refs?

  source_kind
  selection_reason
  request_reason?
  scope?
  authority?
  confidence?

  provenance
  invalidation_keys?
  warnings?
}
```

`id` is stable enough to identify this candidate in the provider response and
builder output event. It does not have to be durable across sessions unless the
provider advertises that behavior.

`provider_id` must match the bundle provider unless a composed provider is
explicitly preserving a sub-provider identity in an advertised extension.

`content` is normalized material the builder may render.

`rendered_text` is provider-owned text that should be preserved if accepted.
This is useful when the provider knows the exact framing needed for a source.

`refs` point to dereferenceable files, URLs, memory records, artifacts,
messages, or provider-native sources.

`source_kind` identifies the source category. Base values are descriptive
strings such as:

```text
file
url
memory
conversation_record
tool
runtime
skill
provider
```

`selection_reason` explains why the candidate was selected. Examples:

```text
startup_snapshot
semantic_recall
explicit_reference
path_evidence
session_resume
scope_change
tool_result_followup
provider_policy
```

`request_reason` echoes or refines the request `reason` when useful. It is
evidence about why the candidate was selected, not a lifecycle signal.

`scope` describes where the candidate applies. Scope is distinct from layer.

Examples:

```text
global
user
project
workspace_root
workspace_subtree(path="backend/")
session
turn
model_call
artifact(ref="...")
```

A candidate may be retained but scoped:

```text
retained.project_instructions
scope = workspace_subtree("backend/")
source = "backend/AGENTS.md"
```

`authority` is an optional provider judgment about how strongly the builder
should treat the candidate, such as instruction, preference, evidence, or
advisory. The builder decides final placement and conflict handling.

`confidence` is optional. If present, it should be provider-local and should
not be compared numerically across providers unless a richer contract says how.

`warnings` are candidate-local warnings such as truncation, sanitization,
partial source read, unsafe source markers, or ambiguity.

The candidate shape intentionally does not include priority, token budget,
latency budget, phase, or placement fields. Ranking, budget, and final placement
belong to the context builder in `context_provider.v0`.

## Provenance

Every candidate must include provenance.

If a candidate is derived from an external source, provenance must identify that
source. If a candidate is derived only from runtime facts, provenance must say
which runtime facts were used.

Sketch:

```text
ContextProvenance {
  source_kind
  source_id?
  uri?
  path?
  range?
  read_at?
  mtime?
  content_hash?
  transformation?
  provider_metadata?
}
```

File-derived provenance should include, when available:

- workspace/root identity
- absolute or workspace-relative path
- read time
- mtime or content hash
- byte, line, or char range when partial
- transformation kind: `exact`, `sanitized`, `truncated`, `summarized`,
  `rendered`, or `reference_only`
- whether the provider read the file or only returned a ref

The provider must not claim exact provenance for summarized, sanitized,
truncated, or otherwise transformed content.

## Invalidation

`invalidation_keys` are optional hints that help the builder decide when
retained context should be reconsidered.

Examples:

```text
file:/workspace/AGENTS.md
file_hash:sha256:...
workspace_root:/workspace
session:sess_...
memory_backend:profile
skill:python
```

The builder owns active-context tracking and invalidation policy. Providers
only supply enough information for that policy to be possible.

## Side Effects

`context_provider.initialize` and `context_provider.get_context` may:

- read allowed sources
- query memory/search backends
- refresh local caches
- refresh indexes or watches needed for retrieval
- return candidates, references, warnings, or failure

It must not silently:

- append session records
- save durable memories
- delete or redact memory
- mutate project files
- change tool permission state
- decide final model input placement

Those behaviors require separate advertised actions or other capability
contracts.

## Security And Policy

The provider should not assume it can read arbitrary paths just because a path
appears in `current_message`, `refs`, or `touched_paths`.

The base request does not include `allowed_read_scopes`, `denied_read_scopes`,
or a `read_policy` object. In `context_provider.v0`, permission enforcement is
owned by the mediator/runtime and the concrete service configuration. If a
provider cannot safely read a source, it should omit the candidate or fail
explicitly rather than bypassing policy.

Source text may be adversarial. Providers should preserve provenance and
warnings so builders can place source material with appropriate authority.

## Failure Semantics

Expected failure categories:

- provider unavailable
- request outside allowed scope
- conversation record access denied
- source file missing
- referenced content changed or cannot be validated
- index unavailable
- retrieval timeout
- candidate too large
- candidate cannot be dereferenced
- unsafe source content
- provider-specific memory/search failure

Failure should be explicit and degradable. A provider may return empty
candidate arrays when it has nothing useful. A provider may return partial
candidates with candidate-local warnings.

A provider must not return known stale content as usable context. It should
either revalidate the source, return a fresh candidate, return a
reference-only candidate that says it was not read, or fail/omit explicitly.

A provider must not fabricate context to hide retrieval failure.

The runtime or context builder should be able to continue without a provider
unless that provider was configured as required.

## Conversation Record Access

The base request should not include the full conversation record by default.

Reasons:

- conversation history size grows without bound
- many providers only need current message, refs, touched paths, runtime facts,
  or active continuation state
- full-history access is a policy and privacy decision
- the session capability owns canonical conversation-record reads

When broader history is needed, the caller must make the scope explicit through
`conversation_view`, or the provider must request `session.read` through the
mediator if it has that advertised access.

## Memory Interaction

Memory can interact with this contract in three ways:

1. Memory as context provider:

```text
initialize(...) -> retained.memory
get_context(reason=user_message) -> retained.memory, per_call.memory
```

2. Memory as model-facing tool provider:

```text
publish tools such as memory_search, memory_save, memory_forget
handle model tool calls through the tool router/executor
return normal tool-result payloads
```

3. Memory as observer/write sink:

```text
observe completed turns
extract durable facts
sync to backend
mirror explicit memory writes
```

The base context-provider contract only requires the first behavior. Durable
memory writes and model-facing tools are separate advertised surfaces.

## Skills Interaction

Skills are part of the context vocabulary.

Examples:

- `retained.skills`: compact skill index or always-on skill guidance
- `per_call.skills`: loaded skill body, selected references, or skill-specific
  instructions needed for the immediate call

The provider may know how to discover installed skills, rank applicable skills,
load skill files, or return skill references. The builder decides where and how
to materialize those candidates.

## Builder Interaction

Provider events say what context was available. Builder events say what the
model actually saw.

Example builder event:

```text
context.built {
  retained_included[]
  per_call_included[]
  omitted[]
  truncated[]
  dereferenced[]
  rendered_messages_or_parts_ref
  cache_partitioning
}
```

The context provider must not depend on every returned candidate being included
in the final model input.

## Concurrency And Idempotency

`context_provider.initialize` and `context_provider.get_context` are read-style
and should be safe to retry.

`request_id` is a correlation identifier, not a durable write idempotency key.
Repeated equivalent requests may produce different candidates if underlying
sources changed. Providers should use provenance and invalidation metadata to
make that difference observable.

If a provider maintains caches or indexes, concurrent requests must not corrupt
provider state or return internally inconsistent candidate payloads.

## Persistence Expectations

The base contract does not require a provider to persist anything.

If a provider has persistent indexes, memory stores, or source caches, it should
advertise the persistence behavior separately. The base runtime can only rely
on the candidates and terminal events returned for the current request.

## Replay Semantics

The terminal event payload is the canonical recorded provider output.

Replay can use the recorded `ContextBundle` to know what context candidates
were available at the time. If replay re-invokes the provider instead, the
provider may produce different output when files, memories, indexes, or runtime
facts have changed. Provenance and invalidation metadata should make these
differences explainable.

## Minimal Test Fixtures

A service implementing the base context-provider contract should be testable
with:

- return empty `retained` and `per_call` arrays for a valid request with no
  applicable context
- return `retained.project_instructions` from `context_provider.initialize`
  for a known project instruction source with file provenance
- return late-discovered `retained.project_instructions` from
  `context_provider.get_context(reason=tool_result)` when touched paths reveal
  a scoped instruction source
- return `per_call.memory` for a query-shaped current message
- return candidates derived from pre-resolved `refs` without requiring raw text
  parsing
- preserve `request_id` in `ContextBundle`
- include `provider_id` in every successful bundle and candidate
- include provenance for every candidate
- avoid full conversation-record access when `conversation_view` is absent
- accept optional `session_id` and `turn_id`
- call `context_provider.initialize` for baseline context instead of encoding
  initialization as `is_first_turn` or `reason=context_initialization`
- accept unknown `reason` values and treat them as retrieval evidence, not
  lifecycle commands
- accept optional `runtime.cwd` and `runtime.current_date`
- accept optional `workspace_roots`
- accept optional `touched_paths` as request-time evidence without requiring
  session persistence
- avoid returning known stale source content as usable context
- avoid committing durable memory or session writes during `initialize` or
  `get_context`
