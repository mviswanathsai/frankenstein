# Context Provider Capability Contract

Date: 2026-08-22

Contract version: `context_provider.v0.2`.

Status: draft.

This is a lightweight contract sketch, not a wire protocol or OpenAPI-style
schema. It names the minimum outside-visible promises a context provider
service must make so the rest of an agentic harness can use it without knowing
its internals.

Design rationale and Hermes evidence live in
`docs/context-provider-service-dossier.md`. This document is the capability
surface.

`context_provider.v0.2` replaces the two-action, layered shape of v0.1 with
one action and one flat offering. The `initialize` action is gone; the
`retained`/`per_call` lifetime split and the `referenced` channel are gone.
Stable context — agent identity, configured instructions, skill indexes, tool
guidance — is runtime-loaded configuration that flows directly to the context
builder. The provider owns only dynamic context: material that must be found
during a session. Placement and retention are builder decisions, not provider
labels.

## Purpose

The context provider supplies dynamic context candidates during a session. The
harness splits context by how it becomes known:

- what the runtime already knows — agent identity, configured instructions,
  workspace grants, tool guidance — is loaded at startup and passed directly
  to the context builder as stable material
- what must be found during the session — memory recall for the current
  message, an instruction file discovered in a directory a tool just touched,
  query-shaped retrieval — is this capability's job

The provider does not decide what the model sees, where it is placed, or how
long it is retained. It returns candidates with honest accounting of the
evidence it was given. The context builder owns placement, retention, and
final materialization.

The provider does not own the session's canonical conversation record,
long-term ordered storage, or final model input. It may receive a triggering
record as request evidence, but broader session reads and final input
materialization remain separate capability operations.

## Owned State

A context provider may own implementation-private state such as:

- indexes
- watches
- caches
- memory backend handles
- provider-native source IDs
- source freshness metadata
- tool schemas if it also advertises model-facing tools

The base context-provider action is observational from the harness point of
view. Calling `context_provider.get_context` must not silently commit durable
semantic state such as new memories, profile facts, session records, or
workspace changes.

If a service wants durable writes, model-facing tools, memory observation,
forget/redact operations, or index rebuild commands, those are separate
advertised surfaces.

## The Offering

A response carries the candidates the provider currently wants considered,
with honest accounting of the evidence it was given. This contract takes no
position on offering granularity: a response may be the provider's complete
current view, a delta against an earlier response, or only current-turn
relevance. That choice is provider policy.

The contract likewise takes no position on retention. Whether a candidate
persists in model input across calls, and how its disappearance is expressed,
is a pairing concern between a provider implementation and a builder
implementation, not a contract promise. The reference provider documented in
the service dossier returns its complete current offering and pairs with a
builder that supersedes on change; a current-only provider pairs with a
current-only builder. Neither direction is legislated here.

The provider does not label candidates by lifetime or placement. There is no
contract-level notion of retained, per-call, or mid-session context.

## Required Action

### `context_provider.get_context`

Return dynamic context candidates for the current request.

There is one action, called per turn and whenever the kernel needs fresh
dynamic context, for example after tool execution. The first call of a
session is not a distinct action; a caller that wants to signal session start
may pass `reason: "session_start"` as evidence.

Base input payload:

```text
ContextRequest {
  id
  session_id?

  transcript?: SessionRecord[]
  refs?: ContextRef[]
  touched_paths?: TouchedPath[]

  reason?
  runtime?: RuntimeFacts
  workspace_roots: WorkspaceRoot[]
}
```

`id` is required and identifies this request. The response and terminal events
refer to it as `request_id`.

`session_id` is the active session identity when known. Correlation only;
receiving it grants no session record access.

`transcript` is an optional window of session records supplied as request
evidence. The caller chooses the window; there is no default full-history
pass, and the provider must not infer record access from `session_id` alone.

`refs` are pre-resolved references from the frontend, gateway, runtime,
session record, or another upstream component. The provider should not be the
primary parser for raw user text references. `ContextRef` is the shared type
established by `session.v0.3`; this contract does not redefine it.

`touched_paths` is optional request-time evidence from the runtime or tool
executor. It is not a canonical session-record field. `TouchedPath` is shared
vocabulary produced by the tool invocation capability; its definition below
may relocate to the tool invocation contract when that contract is revised.

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
`unknown`. These are descriptive strings, not a closed enum.

`reason` is optional free-form metadata explaining why the action was invoked.
It gives the implementation retrieval and ranking context. It is evidence,
not a command, and providers are not required to branch on it.
`session_start`, `user_message`, and `tool_result` are useful conventions,
not a closed enumeration. Unknown reasons must be accepted and may be treated
as generic.

`runtime` carries small runtime facts that are clearly known by the caller:

```text
RuntimeFacts {
  cwd?
}
```

`cwd` is the caller-resolved working directory for this invocation. It is the
single base for relative-path resolution and does not grant or limit
filesystem access. The provider does not resolve a competing cwd from session
metadata or another source. No other runtime facts are part of this floor; a
richer service may advertise more.

`workspace_roots` is the complete filesystem read boundary granted to the
provider for this invocation. It is required but may be empty; an empty list
grants no filesystem reads beyond what is reachable under the cwd. The caller
may add or remove roots on later calls.

```text
WorkspaceRoot {
  path
}
```

`path` is required because it is the boundary the provider must enforce. It
must be absolute and resolved to a canonical location before access checks so
relative components and symlinks cannot escape the granted root.

Terminal events:

- `context_provider.context_provided`
- `context_provider.context_failed`

## Response Payload

Successful output payload:

```text
ContextOffering {
  request_id
  candidates: ContextCandidate[]
  failures: string[]
}
```

`request_id` references the `id` of the request that produced this offering.

`candidates` is a required ordered list and may be empty. Ordering
communicates the provider's relative preference across the whole list.

`failures` is a required array of human-readable strings and may be empty.
Each entry explains one input ref the provider did not dereference and why,
in input order. Entries are plain text; this contract names no code
vocabulary for them. They are non-terminal: the provider may still return the
rest of the offering.

The base response intentionally does not include general-purpose `issues`,
`warnings`, or `degradations` arrays. A terminal provider failure should use
`context_provider.context_failed`.

Failure output payload:

```text
ContextFailure {
  request_id
  code
  message?
  retryable
}
```

`request_id` is required and echoes the request. `code` is required.
`retryable` is a required plain boolean; this action is read-style, so a
service that cannot determine retryability should report `true`.

Base failure codes are `invalid_request`, `service_unavailable`, and
`internal_failure`. A service may return namespaced codes for richer cases.

## Candidate Shape

```text
ContextCandidate {
  id
  metadata?: map[string]any
  content: string
  refs?: ContextRef[]
}
```

`id` identifies the candidate in the rendered model input and in events, so
the model and the record can refer to a candidate without inlining it. It
should be stable for the same logical candidate across responses within a
provider lifecycle and unique within a response; it does not have to be
durable across sessions unless the provider advertises that behavior.

`content` is required, must be non-empty, and is the provider-prepared text it
wants the builder to consider. It may be exact source text, synthesized text,
or text with provider-selected framing. The candidate does not expose separate
normalized, raw, and rendered variants. The builder still decides whether to
preserve, transform, or omit the content.

`refs` point to dereferenceable files, URLs, memory records, artifacts,
messages, or provider-native sources from which the candidate was produced.
They identify source material and never substitute for candidate content.

The candidate shape intentionally does not include named priority, token
budget, latency budget, phase, lifetime, or placement fields. List ordering is
the provider's preference; ranking, budget, and final placement belong to the
context builder.

`metadata` is an optional advisory map of string keys to plain string or
structured JSON values. It is the per-implementation extension seam: a
provider may attach hints it believes
a builder could use — for example, a role convention the reference provider
emits so builders can section rendered output. Metadata is non-normative. No
guarantee in this contract depends on the presence or value of any key, and a
conforming builder may ignore metadata entirely. There is no closed key
vocabulary here; conventions the reference provider emits are documented in
the service dossier. A key that earns cross-implementation meaning may be
promoted to a named field in a later contract version.

## Accounting

Every explicit input ref must appear among some candidate's `refs` or be
identified in `failures`. A provider must not silently drop an input ref or
return a candidate whose refs substitute for content.

## Side Effects

`context_provider.get_context` may:

- read allowed sources
- query memory/search backends
- refresh local caches
- refresh indexes or watches needed for retrieval
- return candidates or failure
- watch context files

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
`transcript` record refs or `touched_paths`, was allowed by an earlier
request, or exists in a provider cache or index.

In `context_provider.v0.2`, `workspace_roots` is the complete filesystem read
policy visible to the provider. Richer grants, denied scopes, source-specific
permissions, and mediator-controlled reads may replace it in a later contract.
If the provider cannot safely resolve or read a source within the current
boundary, it should omit the candidate or fail explicitly rather than
bypassing the boundary.

Source text may be adversarial. Candidate `refs` identify source material when
that information is available, but the builder remains responsible for
deciding how to treat and place candidate content.

## Failure Semantics

Expected failure categories:

- provider unavailable
- request outside allowed scope
- source file missing
- referenced content changed or cannot be validated
- index unavailable
- retrieval timeout
- candidate too large
- candidate cannot be dereferenced
- unsafe source content
- provider-specific memory/search failure

Failure should be explicit and degradable. A provider may return an empty
candidate list when it has nothing useful. A provider may omit an unusable
candidate without failing an otherwise useful offering.

Failure to dereference an individual input ref should normally be reported in
`failures`. It does not require the terminal `context_provider.context_failed`
event.

A provider must not return known stale content as usable context. It should
either revalidate the source, return a fresh candidate, report the unread ref
in `failures`, or omit a candidate that was not derived from an explicit
input ref.

A provider must not fabricate context to hide retrieval failure.

The runtime or context builder should be able to continue without a provider
unless that provider was configured as required.

## Conversation Record Access

The base request should not include the full conversation record by default.

Reasons:

- conversation history size grows without bound
- many providers only need the current message, its refs, touched paths, or
  runtime facts
- full-history access is a policy and privacy decision
- the session capability owns canonical conversation-record reads

When broader history or active continuation state is needed, the provider must
request `session.get` through the mediator if it has that advertised access.
Merely receiving a `session_id` does not grant session record access.

## Skills Interaction

Skills are part of the context vocabulary. A dynamic skills candidate may
carry a loaded skill body, a selected reference, or skill guidance needed for
the current message, in the candidate list. Stable skill indexes are
runtime-loaded configuration passed to the builder directly, not provider
output.

## Builder Interaction

Provider events say what dynamic context was available. Builder events say
what the model actually saw. The builder owns delivery, retention, and
omission; the provider must not depend on every returned candidate being
included in the final model input.

A provider's offering granularity and a builder's delivery policy are a
pairing choice, not a contract coupling. Providers and builders are composed
by the runtime and evaluated as pairs; a provider that assumes persistence
across calls pairs only with a builder that provides it.

## Concurrency And Idempotency

`context_provider.get_context` is read-style and should be safe to retry.

The request `id` is an identity and correlation value, not a durable write
idempotency key. `ContextOffering.request_id` and
`ContextFailure.request_id` refer back to it. Repeated equivalent requests
may produce different candidates if underlying sources changed. The contract
does not require candidates to explain why their content differs between
requests.

If a provider maintains caches or indexes, concurrent requests must not
corrupt provider state or return internally inconsistent candidate payloads.

## Extension Surface

This contract is the floor. A generic builder relies only on the fields named
here. A richer provider may advertise additional actions, additional
candidate fields, candidate-level hints in `metadata`, or negotiated shapes
with a compatible builder; those ride on top of this surface and must not
change the meaning of the floor fields. A later version may add a
contribution seam for plugins; nothing here precludes it.

## Open Questions

- Whether `workspace_roots` enforcement belongs to the provider (current
  stance) or to the mediator/runtime, with roots as caller-granted evidence.
  Under discussion.
- Whether `TouchedPath` relocates to the tool invocation contract or a shared
  types document, since tool invocation is its producer.
- Whether emitted snapshots should become durable transcript records. This
  question follows the reference builder's delivery policy and has relocated
  to the context-builder dossier.

## Minimal Test Fixtures

A service implementing the base context-provider contract should be testable
with:

- return an empty candidate list and empty failures for a valid request with
  no applicable context
- return candidates ordered by the provider's relative preference
- return memory candidates for a query-shaped triggering record
- return project-instruction candidates when touched paths reveal a relevant
  instruction source
- return content-bearing candidates derived from pre-resolved request refs
  without requiring raw text parsing
- account for every explicit input ref through a candidate's `refs` or an
  entry in `failures`, in input order
- return an otherwise successful offering when one input ref cannot be
  dereferenced
- preserve `ContextRequest.id` as `ContextOffering.request_id` and in the
  failure payload
- require non-empty `content` on every candidate and treat candidate `refs`
  only as source links
- accept optional candidate `metadata` with no floor behavior depending on
  any key
- keep candidate ids stable across responses within a provider lifecycle and
  unique within a single response
- accept an optional `session_id`
- accept unknown `reason` values and treat them as retrieval evidence, not
  lifecycle commands
- accept optional `runtime.cwd`
- require `workspace_roots` and accept an empty list as no filesystem access
- resolve relative paths from `runtime.cwd` without treating cwd as permission
- reject or omit filesystem reads outside the current request's
  `workspace_roots`
- honor roots added or removed between requests without reusing stale access
  from provider caches or indexes
- accept optional `touched_paths` as request-time evidence without requiring
  session persistence
- avoid returning known stale source content as usable context
- avoid committing durable memory or session writes during `get_context`
