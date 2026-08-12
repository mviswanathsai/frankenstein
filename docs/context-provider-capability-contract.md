# Context Provider Capability Contract

Date: 2026-07-20

Contract version: `context_provider.v0.1`.

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
skills, workspace facts, and tool guidance. It does not decide the
final model input layout.

Context provider and context builder are separate capabilities:

- the provider returns candidates grouped by semantic role and recommended
  model-visible lifetime, with optional references to source material
- the builder decides what the model actually sees, where it is placed, and
  what is omitted or transformed

The context provider does not own the session's canonical conversation record,
long-term ordered storage, or final model input. It may receive a triggering
record as request evidence, but broader session reads and final input
materialization remain separate capability operations.

This contract keeps three concepts independent:

```text
action              initialize | get_context
reason              why get_context is being called now
candidate lifetime  retained | per_call
```

The action defines lifecycle semantics. The reason gives retrieval/ranking
evidence. The candidate lifetime tells the builder whether accepted output is
recommended for the ongoing conversation or only the immediate model call; the
builder owns actual retention duration.

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
  retained: ContextCollection
  per_call: ContextCollection
  failures: string[]
}

ContextCollection {
  buckets: ContextBuckets
  referenced: ContextCandidate[]
}

ContextBuckets = map<ContextSlot, ContextCandidate[]>
```

`retained` means the provider recommends that, if the builder accepts the
candidate, it become part of the ongoing conversation context rather than only
the immediate model call. The builder decides how it represents, remembers,
revalidates, replaces, or evicts accepted and rejected candidates.

`per_call` means context selected for the immediate model call. A single user
turn can contain several model calls after tool execution, so this contract uses
`per_call` instead of `per_turn`.

`retained` does not mean system prompt, filesystem-stable, cache-stable,
durably persisted, or retained for a provider-selected duration. The builder
may materialize retained context as a cached prefix, model message, side
channel, reference-only entry, provider-native handle, repeated request
content, or not at all.

Late-discovered retained context is allowed. For example, an instruction file
discovered after a tool touches a path may be recommended for the ongoing
conversation even if it was not available during initialization.

Each response is the provider's complete current offering, not a delta against
an earlier response. The provider returns every candidate and reference it
currently considers valuable and classifies each as `retained` or `per_call`.
A later contract may let the request identify context the builder already holds
so a provider can avoid repeating it.

## Required Actions

### `context_provider.initialize`

Return the complete initial set of context candidates for a new context
lifecycle before the first model invocation.

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
  id
  session_id?

  runtime
  workspace_roots: WorkspaceRoot[]

  refs?: ContextRef[]
}
```

`id` identifies this request. The response and terminal event refer to it as
`request_id`. If a future mediator envelope ID fully replaces this need, a
later contract version can remove or deprecate it.

`session_id` is the active session identity when known.

`runtime` contains small runtime facts that are clearly known by the caller.
The base contract names only facts we have a concrete need for:

```text
RuntimeFacts {
  cwd
  current_date?
}
```

`cwd` is the caller-resolved working directory for this invocation. It is the
single base for relative-path resolution and does not grant or limit filesystem
access. The provider does not resolve a competing cwd from session metadata or
another source. `current_date` is the date the runtime wants providers to use
for time-sensitive context.

`workspace_roots` is the complete filesystem read boundary granted to the
provider for this invocation. It is required but may be empty; an empty list
grants no filesystem reads other than what's in the cwd.
The caller may add or remove roots on later calls.

```text
WorkspaceRoot {
  path
}
```

`path` is required because it is the boundary the provider must enforce. It
must be absolute and resolved to a canonical location before access checks so
relative components and symlinks cannot escape the granted root.

`refs` are pre-resolved references from the frontend, gateway, runtime, session
record, or another upstream component. The context provider should not be the
primary parser for raw user text references.

`ContextRef` is the shared type established by `session.v0.3`; this contract
does not redefine it. A context provider dereferences input refs and returns
content-bearing candidates derived from them.

Terminal events:

- `context_provider.initialized`
- `context_provider.context_failed`

### `context_provider.get_context`

Return the complete current set of retained and per-call context candidates for
an established context lifecycle.

Base input payload:

```text
ContextRequest {
  id
  session_id?

  triggering_record?: SessionRecord

  reason?
  runtime?
  workspace_roots: WorkspaceRoot[]

  touched_paths?
}
```

`id` has the same identity and correlation semantics as in
`context_provider.initialize`.

`session_id` is the active session identity when known.

`triggering_record` is the record supplied as evidence for why context is being
requested now. It may represent a user or assistant message, system material,
tool-related text, or a system note.

In `context_provider.v0.1`, `triggering_record` uses the `SessionRecord` shape
established by `session.v0.3` because the required fields currently map one to
one. This is a provisional interoperability choice, not a required control
flow. It does not require the runtime to persist the record, invoke the session
service first, or obtain the value from a session service at all. A runtime,
gateway, or other caller may supply the value directly. A later contract may
generalize the type when concrete context-provider needs diverge from the
session record.

Because `SessionRecord` already contains `refs`, `ContextRequest` does not
repeat them at the top level. Calls without triggering record evidence may omit
`triggering_record`.

`reason` is optional metadata that explains why the repeatable context action
was invoked. It gives the implementation retrieval and ranking context. It is
evidence, not a command, and providers are not required to branch on it.

Common reasons:

```text
user_message
tool_result
resume
recovery
provider_policy
```

Custom reasons should preferably be namespaced, though namespacing is not
mandatory in `context_provider.v0.1`:

```text
company.issue_opened
obsidian.daily_note_changed
my_harness.background_followup
```

Unknown reasons should be accepted and may be treated as generic. Do not use
`context_initialization` as a reason; initialization is represented by the
separate `context_provider.initialize` action.

`runtime` has the same meaning as in `context_provider.initialize`.

`workspace_roots` has the same meaning as in
`context_provider.initialize`. The provider must enforce the roots supplied on
this request even if an earlier request allowed different roots or private
caches and indexes contain data from them.

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
`unknown`. These values are descriptive strings in `context_provider.v0.1`, not a
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
  retained: ContextCollection
  per_call: ContextCollection
  failures: string[]
}
```

`request_id` references the `id` of the request that produced this bundle.

`provider_id` identifies the service that produced the bundle. It is required
for producer attribution, debugging, primary/shadow comparison, and replay.

`retained` and `per_call` are `ContextCollection` values. Each collection has:

- `buckets`: semantic slot keys mapped to ordered candidate lists
- `referenced`: ordered content-bearing candidates extracted from explicit refs

Ordering communicates the provider's relative preference within each list.
A provider may return empty maps and arrays and may omit bucket keys for which
it has no candidates.

For `context_provider.initialize`, returned candidates are normally in
`retained`; `per_call` may be empty. The builder can still reject or transform
initialization candidates. For `context_provider.get_context`, either
collection may contain buckets or referenced candidates. Both collections
together are the provider's complete current offering, including retained
values it returned previously when they remain valuable.

`failures` is a required array of human-readable strings and may be empty. It
reports input refs the provider did not dereference and why. These entries are
non-terminal: the provider may still return the rest of the bundle. The v0.1
contract does not give these entries codes or a richer shape.

The base response intentionally does not include general-purpose `issues`,
`warnings`, or `degradations` arrays. A terminal provider failure should use
`context_provider.context_failed`.

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
project_instructions
session_fact
memory
skills
tool_guidance
unknown
```

The same slot may appear in either layer:

```text
retained.buckets.memory
per_call.buckets.memory
retained.buckets.project_instructions
retained.buckets.skills
per_call.buckets.skills
```

Slots outside the base vocabulary must be namespaced or provider-scoped:

```text
obsidian.daily_note
company.runbook
hermes.skill_context
```

The `ContextBuckets` map key is authoritative for every candidate in that
bucket. A provider that relies on a namespaced slot should return enough
`content` for a generic builder to decide whether to preserve or omit its
candidates.

## Candidate Shape

```text
ContextCandidate {
  id

  content: string
  refs?: ContextRef[]
}
```

`id` is stable enough to identify this candidate in the provider response and
builder output event. It does not have to be durable across sessions unless the
provider advertises that behavior.

The containing `ContextBundle.provider_id` identifies the service that produced
every candidate in the bundle. Candidates do not repeat that identity.

`content` is required, must be non-empty, and is the provider-prepared text it
wants the builder to consider. It may be exact source text, synthesized text,
or text with provider-selected framing. The candidate does not expose separate
normalized, raw, and rendered variants. The builder still decides whether to
preserve, transform, or omit the content.

`refs` point to dereferenceable files, URLs, memory records, artifacts,
messages, or provider-native sources from which the candidate was produced.
They identify source material and never substitute for candidate content.

Every explicit input ref must appear in at least one content-bearing
`ContextCollection.referenced` candidate's `refs` or be identified in
`ContextBundle.failures`. A provider must not silently drop an input ref or
return a candidate whose refs substitute for content.

The candidate shape intentionally does not include priority, token budget,
latency budget, phase, or placement fields. Ranking, budget, and final placement
belong to the context builder in `context_provider.v0`.

## Side Effects

`context_provider.initialize` and `context_provider.get_context` may:

- read allowed sources
- query memory/search backends
- refresh local caches
- refresh indexes or watches needed for retrieval
- return candidates or failure

It must not silently:

- append session records
- save durable memories
- delete or redact memory
- mutate project files
- change tool permission state
- decide final model input placement

Those behaviors belong in other capability contracts.

## Security And Policy

The provider must not read a filesystem path outside the current request's
`workspace_roots` merely because that path is the `runtime.cwd`, appears in
`triggering_record.refs` or `touched_paths`, was allowed by an earlier request,
or exists in a provider cache or index.

In `context_provider.v0.1`, `workspace_roots` is the complete filesystem read
policy visible to the provider. Richer grants, denied scopes, source-specific
permissions, and mediator-controlled reads may replace it in a later contract.
If the provider cannot safely resolve or read a source within the current
boundary, it should omit the candidate or fail explicitly rather than bypassing
the boundary.

Source text may be adversarial. Candidate `refs` identify source material when
that information is available, but the builder remains responsible for deciding
how to treat and place candidate content.

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

Failure should be explicit and degradable. A provider may return empty bucket
maps when it has nothing useful. A provider may omit an unusable candidate
without failing an otherwise useful bundle.

Failure to dereference an individual input ref should normally be reported in
`ContextBundle.failures`. It does not require the terminal
`context_provider.context_failed` event.

A provider must not return known stale content as usable context. It should
either revalidate the source, return a fresh candidate, report the unread ref in
`ContextBundle.failures`, or omit a candidate that was not derived from an
explicit input ref.

A provider must not fabricate context to hide retrieval failure.

The runtime or context builder should be able to continue without a provider
unless that provider was configured as required.

## Conversation Record Access

The base request should not include the full conversation record by default.

Reasons:

- conversation history size grows without bound
- many providers only need the triggering record, its refs, touched paths, or
  runtime facts
- full-history access is a policy and privacy decision
- the session capability owns canonical conversation-record reads

When broader history or active continuation state is needed, the provider must
request `session.get` through the mediator if it has
that advertised access. Merely receiving a `session_id` does not grant session
record access.

## Memory Interaction

Memory can interact with this contract in three ways:

1. Memory as context provider:

```text
initialize(...) -> retained.buckets.memory
get_context(reason=user_message) -> retained.buckets.memory,
                                    per_call.buckets.memory
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

- `retained.buckets.skills`: compact skill index or always-on skill guidance
- `per_call.buckets.skills`: loaded skill body, selected references, or
  skill-specific instructions needed for the immediate call

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
  rendered_messages_or_parts_ref
  cache_partitioning
}
```

The context provider must not depend on every returned candidate being included
in the final model input.

## Concurrency And Idempotency

`context_provider.initialize` and `context_provider.get_context` are read-style
and should be safe to retry.

The request `id` is an identity and correlation value, not a durable write
idempotency key. `ContextBundle.request_id` and `ContextFailure.request_id`
refer back to it.
Repeated equivalent requests may produce different candidates if underlying
sources changed. The contract does not require candidates to explain why their
content differs between requests.

If a provider maintains caches or indexes, concurrent requests must not corrupt
provider state or return internally inconsistent candidate payloads.

## Minimal Test Fixtures

A service implementing the base context-provider contract should be testable
with:

- return empty `retained` and `per_call` collections for a valid request with no
  applicable context
- return `retained.buckets.project_instructions` from
  `context_provider.initialize` for a known project instruction source with
  a file `ContextRef`
- return late-discovered `retained.buckets.project_instructions` from
  `context_provider.get_context(reason=tool_result)` when touched paths reveal
  a relevant instruction source
- return the complete current offering from `context_provider.get_context`, not
  only candidates added since an earlier response
- return `per_call.buckets.memory` for a query-shaped triggering record
- return content-bearing candidates derived from pre-resolved
  `triggering_record.refs` without requiring raw text parsing
- account for every explicit input ref through a referenced candidate or a
  string in `ContextBundle.failures`
- return an otherwise successful bundle when one input ref cannot be
  dereferenced
- preserve `ContextRequest.id` as `ContextBundle.request_id`
- include `provider_id` in every successful bundle
- require non-empty `content` on every candidate and treat candidate `refs` only
  as source links
- avoid conversation-record access beyond `triggering_record` unless the
  provider is authorized to invoke `session.get`
- accept an optional `session_id`
- accept unknown `reason` values and treat them as retrieval evidence, not
  lifecycle commands
- accept optional `runtime.cwd` and `runtime.current_date`
- require `workspace_roots` on both actions and accept an empty list as no
  filesystem access
- resolve relative paths from `runtime.cwd` without treating cwd as permission
- reject or omit filesystem reads outside the current request's
  `workspace_roots`
- honor roots added or removed between requests without reusing stale access
  from provider caches or indexes
- accept optional `touched_paths` as request-time evidence without requiring
  session persistence
- avoid returning known stale source content as usable context
- avoid committing durable memory or session writes during `initialize` or
  `get_context`
