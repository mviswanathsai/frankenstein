# Tool Invocation Capability Contract

Date: 2026-08-01

Contract version: `tool_invocation.v0`.

Status: draft.

This document defines what the rest of the harness may expect from a Tool
Invocation service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning live in
`docs/tool-invocation-service-dossier.md`.

## Plain-Language Terms

This contract uses a few repeated terms:

- **catalog**: the complete list of tools currently offered to the model
- **backend**: the function, capability, process, MCP server, or remote system
  that actually carries out a tool
- **canonical**: the harness-wide identity or value after provider-specific
  names and formats have been converted
- **normalized**: converted from a backend- or provider-specific format into a
  contract shape used by the rest of the harness
- **publication eligibility**: whether a registered tool is enabled, valid,
  authorized, policy-allowed, and otherwise eligible to appear in a catalog
- **runtime availability**: whether an eligible tool's backend can accept and
  complete a call now; this may change after catalog construction
- **authority**: caller authorization supplied by the runtime or deployment,
  never by the model; resource containment belongs to the runtime-supplied
  execution sandbox
- **side effect**: a change outside the returned result, such as writing a
  file, sending a message, or updating memory
- **idempotency key**: a duplicate-protection key used to recognize a retried
  execution request
- **ref**: a `ContextRef` link to material such as a file, URL, or artifact
- **terminal event**: the final recorded success or failure for one command
- **mediator**: the harness layer that validates and routes commands and records
  their events

## Purpose

Tool Invocation is the controlled boundary between model-written tool calls and
the systems that carry them out.

It gives the harness one place to:

- obtain the tools that may currently be shown to the model
- validate calls returned by the model
- apply tool policy and approval rules
- dispatch calls to built-in tools, plugins, other capabilities, MCP servers,
  or remote backends
- control concurrency, cancellation, and timeouts
- normalize results for the model, runtime, session, and event log
- record whether side effects are known to have happened

The base flow is:

```text
runtime -> tool_invocation.list_tools
runtime -> model invocation with returned tool definitions
model invocation -> normalized tool calls
runtime -> tool_invocation.execute
tool invocation -> normalized tool results
runtime -> session/context/model continuation
```

The first implementation may use direct in-process calls through the mediator.
Nothing in this contract requires a separate process or an event bus.

## Boundary

Tool Invocation owns controlled tool publication, call validation and routing,
and normalized execution outcomes. Resource containment belongs to the
runtime-supplied execution boundary.

Tool Invocation is the sole semantic owner and producer of canonical
`ToolDefinition` and `ToolCatalog` values. It decides catalog membership,
ordering, canonical definition contents, revisions, and content-derived catalog
IDs. The Runtime Kernel may request, hold, reuse, replace, and route an immutable
catalog as part of model-call sequencing, but it must not construct or modify
one. Model Invocation may encode a returned catalog for a provider, but it must
not add, remove, or semantically change canonical definitions.

It does not own:

- the agentic loop
- the canonical session record
- the full live turn state
- model-provider request or response formats
- the domain state behind every tool
- global model retry, fallback, or turn-budget decisions

A tool may be a model-facing route to another capability:

```text
memory_save     -> Memory capability
session_search  -> Session capability
clarify         -> UI or Gateway capability
delegate_task   -> Runtime or Scheduler
browser_click   -> Browser backend
remote_search   -> MCP server
```

The Tool Invocation service routes and controls these calls. It does not become
the owner of the target capability's state.

When a tool needs current session, turn, scheduler, UI, or domain state, its
backend calls the capability that owns that state through the mediator. The
runtime binds the needed identities and any caller authorization to those
calls; Tool Invocation does not gather a generic copy of the agent's state.

The runtime also supplies the bounded execution route through which a backend
runs. Tool Invocation does not provision, configure, own, or broaden a sandbox.

## State Ownership

A Tool Invocation service may own:

- registered tool definitions
- canonical tool identities
- canonical catalog construction, membership, ordering, and snapshot identity
- a bounded cache of immutable catalog snapshots keyed by catalog ID
- backend registrations and connections
- current backend availability
- tool-specific policy configuration
- pending approvals
- in-flight tool executions
- concurrency locks and worker limits
- duplicate-detection and idempotency records
- result artifacts it creates

The Runtime Kernel owns the current turn, model-call sequence, remaining turn
budget, global cancellation, the active catalog reference selected from values
returned by Tool Invocation, and the decision to continue or stop the loop.
Holding an active catalog reference does not transfer catalog ownership to the
Runtime Kernel.

The Session capability owns durable conversation continuity. Tool Invocation
returns structured outcomes; the runtime may append normalized text and refs
from those outcomes to Session.

## Shared Types

This contract reuses:

- `ContextRef` from `session.v0.2`
- `TouchedPath` from `context_provider.v0.1`

It does not redefine either shape.

A `ContextRef` identifies a file, directory, URL, artifact, memory record,
session record, or namespaced external resource.

A `TouchedPath` records path evidence such as a read, write, list, or execute
operation. Tool Invocation produces this evidence so the runtime may pass it to
Context Provider after a tool call.

## Model-Written Arguments And Runtime Authority

Every execution has two different sources of input:

```text
model-written:
  tool name
  tool arguments

runtime-supplied:
  session and turn references
  cancellation and deadline
  approval decisions
  authenticated caller identity when authorization is configured
  bounded backend execution route
```

These sources must remain separate.

The model must not be able to grant itself access by placing a `session_id`,
tenant identity, approval decision, sandbox selection, or similar authority
inside ordinary tool arguments.

If a tool legitimately needs a field with the same spelling, that field is
still only tool data. It does not replace runtime-supplied identity,
authorization, approval, or execution containment.

## Runtime Execution Boundary

Cancellation and deadlines are carried by the mediator call rather than copied
into Tool Invocation payloads. Losing the caller connection must request
cancellation, but it does not prove that a started backend stopped.

The runtime supplies an already-bounded route or environment for backend
execution. Filesystem, process, network, device, credential, and external
service containment belong to that execution boundary, not to Tool Invocation.
Tool Invocation must dispatch through the supplied boundary and must not create
or select a broader one.

If the sandbox or target capability denies access, Tool Invocation normalizes
that backend outcome into an explicit call result. It does not retry outside
the boundary or reinterpret denial as permission.

Caller authorization is separate from resource containment. When configured,
Tool Invocation may enforce RBAC or tool policy using authenticated caller facts
supplied by the mediator. Earlier requests, caches, catalogs, or backend
connections must not preserve caller authorization that has since been removed.

## Required Actions

Each action is carried in the project-wide command envelope. The successful
output shown below is the payload of that action's success event. A direct-call
mediator may return the same terminal event to the caller.

### `tool_invocation.list_tools`

Return the complete current model-facing tool catalog for this request.

Input:

```text
ToolCatalogRequest {
  id
  session_id?
  turn_id?
}
```

`id` identifies this request. Terminal output refers to it as `request_id`.

`session_id` and `turn_id` identify the current session and turn when known.
They provide correlation and may affect configured publication eligibility.
They do not grant session-record access.

The service determines the catalog from current configuration, current caller
authorization and policy when configured, registered definition validity, and
the supplied session and turn references. Transient backend health or runtime
availability must not by itself add or remove a catalog entry.

Successful output:

```text
ToolCatalog {
  id
  tools: ToolDefinition[]
}

ToolCatalogListed {
  request_id
  catalog: ToolCatalog
}
```

`ToolCatalog.id` identifies this complete catalog snapshot. A consumer may
retain or reference it as model-input evidence.

`ToolCatalogListed.request_id` refers to `ToolCatalogRequest.id`.

`ToolCatalog.tools` is the complete ordered list currently offered by the
service, not a delta from an earlier catalog. It may be empty.

The catalog ID is content-derived from the exact ordered canonical definitions.
Repeated identical catalogs therefore have the same ID, including across
service restarts. Callers must not infer version order from the ID.

The order is the order the service recommends for model-facing publication.
Model Invocation should preserve it when the selected provider permits. Order
does not grant priority, permission, or execution precedence.

Terminal events:

- `tool_invocation.tools_listed`
- `tool_invocation.list_failed`

### `tool_invocation.execute`

Validate and execute one model-produced batch of tool calls.

Input:

```text
ToolExecutionRequest {
  id
  idempotency_key
  catalog_id
  session_id?
  turn_id?
  mode?
  calls: ToolCall[]
}

ToolExecutionMode = sequential | allow_parallel
```

`id` identifies this execution request. Terminal output refers to it as
`request_id`.

`idempotency_key` is required duplicate protection. Repeating the same request
with the same key must not silently execute its calls again.

`catalog_id` identifies the exact canonical catalog supplied to the model that
produced these calls. It is runtime-supplied model-input evidence, not a
model-written argument and not execution authority. Ordinary calls resolve
against current registration through their tool identity and definition
revision. Catalog-changing calls additionally use this catalog as their
immutable base snapshot.

`session_id` and `turn_id` have the same meaning as in
`tool_invocation.list_tools`.

A request containing a call that can produce a catalog transition must include
both `session_id` and `turn_id`. The service rejects the request as
`invalid_request` before any call starts when either value is absent.

`mode` is optional. Omission means `sequential`.

- `sequential` requires calls to run in input order, one at a time
- `allow_parallel` permits the service to run calls concurrently when it knows
  they are safe

`allow_parallel` does not require parallel execution. The service may still
serialize calls because of shared state, overlapping paths, approval, backend
limits, or unknown effect behavior.

`calls` must contain at least one call.

Successful terminal output:

```text
ToolExecutionResult {
  request_id
  results: ToolResult[]
  catalog_transition?
}

ToolCatalogTransition {
  base_catalog_id
  catalog: ToolCatalog
}
```

`results` contains exactly one result for every input call and preserves input
call order even when execution was concurrent.

Individual tool failure, denial, timeout, or cancellation is represented in its
`ToolResult`. These expected call outcomes do not turn an otherwise complete
batch into a request-level failure.

`catalog_transition` is optional and uses `ToolCatalogTransition` when one or
more successful calls changed the catalog that should be supplied to the next
Model Invocation. `base_catalog_id` must equal
`ToolExecutionRequest.catalog_id`. `catalog` is the complete immutable
replacement snapshot, not a delta. The ordinary `ToolResult` values remain the
model-facing results for the calls that caused the transition.

At most one catalog transition may be returned for an execution batch. When
several calls load tools successfully, the transition contains the final
catalog after applying those loads in input-call order.

Terminal events:

- `tool_invocation.executed`
- `tool_invocation.execution_failed`

## Tool Definition

```text
ToolDefinition {
  id
  revision
  name
  description
  input_schema
}
```

`id` is the stable canonical identity assigned by Tool Invocation. It must not
change merely because a model provider requires a different wire-format name.
It must remain the same across catalog snapshots while the same logical tool
remains registered.

`revision` is a content-derived identity for this exact canonical definition,
including its model-facing name, description, and input schema. It changes when
any of those values changes while `id` may remain stable for the same logical
tool.

`name` is the unique model-facing name within this catalog. It is the name that
normalized `ToolCall` values use.

If a model provider restricts tool names, Model Invocation may encode the name
for that provider. It must map returned calls back to this canonical name before
Tool Invocation receives them.

`description` is required, non-empty model-facing guidance. The service must
bound and sanitize descriptions received from plugins or remote servers before
placing them in a model prompt.

`input_schema` is the JSON Schema for the tool's arguments. Its top-level value
must describe an object. The schema must be valid enough for the service to
validate calls deterministically.

The base definition does not expose:

- backend address
- implementation language
- MCP server identity
- approval rules
- secret requirements
- arbitrary backend metadata

Those details are service-owned and must not be needed by the model.

The base contract does not define an output schema. Tool backends may validate
their native output privately, but Tool Invocation must normalize the
cross-boundary result into `ToolResult`.

## Catalog Rules

- Tool IDs must be unique within the service.
- Tool names must be unique within a catalog.
- A registration or refresh must not silently replace an unrelated tool with
  the same name.
- Deliberate replacement must be explicit and auditable.
- A catalog must not include a tool that is known to be ineligible for
  publication or unauthorized for the current request.
- A tool that remains publication-eligible must not be removed merely because
  its backend is temporarily unavailable. Catalog membership is not a liveness
  guarantee.
- A backend becoming unavailable after publication must produce an explicit
  call result; it must not silently drop the call.
- A catalog is a complete snapshot, not execution authority and not a promise
  that availability can never change.
- Historical catalog retention belongs to model-input evidence and
  observability. Tool Invocation may keep only a bounded in-memory cache of
  immutable catalog snapshots keyed by catalog ID. Ordinary tool execution
  resolves through tool identity and definition revision rather than through a
  historical catalog body.
- Catalog discovery and registration are implementation details. The harness
  depends on `list_tools`, not on a particular plugin or MCP registry.

The base contract does not yet define the provider-to-Tool-Invocation
publication shape. When that shape is introduced, it must distinguish
publication eligibility from runtime availability instead of overloading one
`available` fact for both. These service-facing facts do not belong in the
model-facing `ToolDefinition`.

## Catalog Evidence

A catalog is immutable canonical model-input evidence. Each Model Invocation
record must identify the exact catalog used for that model call. Repeated calls
that use an identical ordered catalog reference the same content-derived ID.

The canonical catalog is distinct from the provider-encoded tool bundle. Model
Invocation must retain that exact encoded bundle, or a durable reference to it,
when provider-level replay is required.

Provider-native late loading does not transfer publication ownership to Model
Invocation. Any definition made callable through a provider-specific search
output, tool reference, or incremental tool item must come from a canonical
catalog produced by Tool Invocation. Model Invocation chooses only the provider
encoding of that publication.

Catalog bodies may be stored once and shared by every event that references
them. A session's callable-catalog timeline and the union of tools ever offered
through the provider's callable-tool mechanism are derived from its ordered
Model Invocation records. Definitions disclosed through ordinary tool-result
text are separate model-input evidence and do not change `ToolCatalog`. Tool
Invocation does not own either history or need it in order to execute calls.

The snapshot cache is not a mutable current catalog and does not transfer the
runtime's active-catalog ownership to Tool Invocation. Catalog construction
always produces a complete snapshot from current registration, authorization,
policy, and publication eligibility. The cache exists only so a
catalog-changing call can use an earlier immutable snapshot as its base.

Cache capacity and eviction policy are implementation configuration. A service
offering catalog-changing calls must retain at least one snapshot. If the
requested base has been evicted, the call returns `catalog_unavailable` without
a transition; the runtime obtains a fresh catalog for a later model invocation.
Tool Invocation does not need durable or unbounded historical catalog storage.

## Progressive Tool Disclosure

A service may offer progressive disclosure through ordinary model-facing tools
named `tool_search`, `tool_describe`, and `tool_load`.

- `tool_search` returns a bounded set of currently discoverable tool names and
  summaries. Searching does not change the active catalog.
- `tool_describe` returns the selected tool's model-facing name, description,
  and input schema. Describing does not change the active catalog.
- `tool_load` asks Tool Invocation to make a discoverable tool directly
  callable in the next model invocation.

The normal reasoning flow may be search, describe, then load, but the contract
does not require a successful description before loading. Tool Invocation must
resolve every load against the current discoverable registry and current caller
authorization, policy, publication eligibility, and definition revision.
Knowing a tool name or having described it does not grant authority. Temporary
backend unavailability does not prevent an otherwise eligible tool from being
loaded; execution still checks current runtime availability.

For a successful `tool_load`, Tool Invocation derives a new catalog from the
immutable catalog identified by `ToolExecutionRequest.catalog_id`. Definitions
from the base catalog that remain publishable retain their relative order.
Definitions that are no longer publishable are omitted, changed definitions use
their current revisions, and newly loaded tools are appended in successful
input-call order. The result's `catalog_transition` carries the complete new
snapshot.

If the requested tool is already present with its current definition, the load
may succeed without returning a catalog transition. If the base catalog cannot
be resolved, the load fails with `catalog_unavailable`. A failed, denied, or
publication-ineligible load does not add its target to a transition.

The model-facing result of `tool_load` remains an ordinary `ToolResult`. The
catalog is not embedded only in `ToolResult.text`; the typed catalog transition
is the runtime-facing control output that makes the tool callable.
For this result, the effective executed tool is `tool_load`; the tool named in
its arguments is a catalog-addition target, not an executed backend target.

The no-proxy flow is:

```text
model -> tool_search
model -> tool_describe
model -> tool_load
tool invocation -> ToolResult plus ToolCatalogTransition
runtime -> next model invocation with transition.catalog
model -> direct call to the loaded tool
```

Progressive disclosure may use a generic execution proxy instead of changing
the callable catalog. The proxy is an ordinary catalogued tool and must not
bypass effective-target validation, authorization, policy, approval, execution
containment, or result rules. A description returned as ordinary result text
may inform a later proxy call but does not make its target provider-natively
callable.

## Tool Call

```text
ToolCall {
  id
  tool_id?
  definition_revision?
  name
  arguments: map<string, json>
}
```

`id` is the harness's canonical identity for this call. It must be unique within
the execution request. Provider-native call and response-item IDs belong to
Model Invocation records, not this shape.

`tool_id` and `definition_revision` are supplied by Model Invocation from the
canonical definition selected for the model request. They are required
together when the returned name mapped to a known definition and absent
together when it did not. They are not model-written arguments.

`name` is the normalized canonical name returned by Model Invocation. When
`tool_id` is present, the name must match that registered tool.

`arguments` is the complete normalized argument object produced from model
output. Tool Invocation resolves the current registration by `tool_id`, checks
that its revision and name match the call, and validates the arguments against
the matching `input_schema`.

If the tool identity is known but its current revision differs from
`definition_revision`, Tool Invocation returns `stale_tool_definition` rather
than executing against a schema the model did not see.

Invalid provider JSON cannot be represented as a command payload. Model
Invocation must report that malformed provider output to the runtime. The
runtime may then create an error result for the model without invoking Tool
Invocation.

Likewise, a call from a model response known to have ended in the middle of
generation must not be submitted for execution merely because repaired
arguments appear valid. The Runtime Kernel and Model Invocation own that
completeness check.

The Tool Invocation service may support limited argument coercion, such as
converting `"5"` to `5`, but:

- coercion must be deterministic and based on the tool schema
- the executed arguments must be the validated coerced arguments
- the service must not invent missing semantic values
- a service may choose strict rejection instead

Argument-repair philosophy is part of the swappable service behavior.

## Tool Result

```text
ToolResult {
  call_id
  tool_id?
  name
  status
  text
  refs: ContextRef[]
  touched_paths: TouchedPath[]
  side_effect
  stop_requested?
  failure?
}

ToolResultStatus =
  succeeded |
  failed |
  denied |
  cancelled |
  timed_out |
  unknown

ToolSideEffect =
  none |
  applied |
  partial |
  unknown

ToolFailure {
  code
  retryable
}
```

`call_id` refers to `ToolCall.id`.

`tool_id` refers to the effective `ToolDefinition.id`. It is required when the
call resolved to a known effective tool. For a generic proxy call, this is the
target tool rather than the proxy definition. It may be absent when the
requested effective target could not be resolved.

`name` is the effective canonical tool name when resolution succeeded. When it
did not, it is the requested direct name or the target name extracted from a
generic proxy call. Proxy indirection is recorded separately so logs and user
surfaces can show both the bridge and the intended target.

`status` means:

- `succeeded`: the tool returned a usable result
- `failed`: the tool is known not to have completed successfully
- `denied`: policy or approval prevented execution
- `cancelled`: cancellation was requested before a normal result was obtained
- `timed_out`: the deadline passed before a normal result was obtained
- `unknown`: the service lost the ability to determine execution outcome

`text` is the required, non-empty, sanitized model-facing result. It is the one
canonical text representation. Raw backend output and a second "normalized"
copy are not part of the base contract.

`refs` is required and may be empty. Large output, images, files, and other
non-text material should normally be represented by a useful text summary plus
one or more refs.

`touched_paths` is required and may be empty. Paths reported here are evidence,
not new authority. Each entry uses `TouchedPath` from
`context_provider.v0.1`.

`side_effect` records what the service knows about external change:

- `none`: the call did not change external or durable state
- `applied`: the intended change is known to have happened
- `partial`: some change happened, but the intended change did not complete
- `unknown`: the service cannot determine whether or how much change happened

Call status and side-effect knowledge are separate. For example:

```text
timed_out + unknown
failed    + partial
denied    + none
succeeded + applied
succeeded + none
```

`stop_requested` is optional. When true, the tool asks the Runtime Kernel not to
start another model/tool iteration after the current batch. The Runtime Kernel
owns the final decision.

`failure` is absent when `status` is `succeeded` and required for every other
status. `code` is stable machine-readable classification. `retryable` says
whether retry could be reasonable in principle; it is not permission to retry
an effectful call automatically.

Base failure codes include:

```text
unknown_tool
invalid_arguments
catalog_unavailable
tool_unavailable
stale_tool_definition
policy_denied
approval_denied
approval_timeout
access_denied
backend_unavailable
backend_failed
malformed_result
result_too_large
cancelled
timed_out
outcome_unknown
```

A service may return namespaced codes for richer cases.

## Request-Level Failure

`tool_invocation.execution_failed` is reserved for failure to produce a complete
batch result. It is not used merely because one tool failed.

Payload:

```text
ToolExecutionFailure {
  request_id?
  code
  message?
  retryable
  results: ToolResult[]
  unresolved_call_ids: string[]
}
```

`request_id` is required when the service could read it from the request. It may
be absent only when failure prevented that.

`results` contains every call outcome the service knows, in input call order.

`unresolved_call_ids` identifies calls for which no reliable `ToolResult` could
be produced, also in input call order. A call ID must not appear in both
collections.

If failure happened before any call started, `results` may be empty and every
input call may be unresolved.

Known outcomes must not be discarded because a sibling call or the service
failed.

`tool_invocation.list_failed` uses:

```text
ToolCatalogFailure {
  request_id?
  code
  message?
  retryable
}
```

Base execution-request failure codes are:

```text
invalid_request
missing_idempotency_key
duplicate_call_id
idempotency_conflict
service_unavailable
internal_failure
```

Base catalog-request failure codes are:

```text
invalid_request
catalog_unavailable
service_unavailable
internal_failure
```

## Intermediate Events

Terminal events are required. `tool_invocation.call_started` is required for
every call that enters a backend. Approval events are required whenever
approval is requested. The execution-started and progress events are optional.

### `tool_invocation.execution_started`

The execution request was accepted for processing.

```text
ToolExecutionStarted {
  request_id
  call_ids: string[]
}
```

### `tool_invocation.call_started`

One call is about to enter its backend.

```text
ToolCallStarted {
  request_id
  call_id
  tool_id
  name
}
```

The mediator must acknowledge that this event was recorded before the backend
is allowed to perform a side effect. If recording cannot be acknowledged, the
call must not enter the backend. Denied or invalid calls do not emit
`call_started`. `tool_id` and `name` identify the effective target. A generic
proxy is not reported as though the proxy itself were the effectful backend.

### `tool_invocation.proxy_dispatch_attempted`

Required when a service offers a generic execution proxy and a model call
provides a parseable target name through it.

```text
ToolProxyDispatchAttempted {
  request_id
  call_id
  proxy_tool_id
  requested_target_name
  effective_tool_id?
  effective_definition_revision?
}
```

`proxy_tool_id` identifies the model-visible proxy definition.
`requested_target_name` is the target name supplied through the proxy.
`effective_tool_id` and `effective_definition_revision` are required together
when that name resolves and absent together when it does not.

This event is emitted before effective-target authorization, approval, or
backend execution. It therefore records proxy use even when later validation or
policy prevents execution. It does not grant authority. Once resolution
succeeds, all later events and the terminal `ToolResult` identify the effective
target.

### `tool_invocation.call_progressed`

Optional human-readable progress for a call that has not completed.

```text
ToolCallProgress {
  request_id
  call_id
  text
}
```

Progress is advisory. It is not a final result and must not be persisted as
though the call completed.

### `tool_invocation.approval_requested`

Policy requires user or operator approval.

```text
ToolApprovalRequest {
  request_id
  call_id
  tool_id
  name
  summary
}
```

`summary` is a required sanitized explanation of the proposed action. It must
contain enough information for a meaningful decision without exposing secrets.

### `tool_invocation.approval_resolved`

An approval request reached a terminal decision.

```text
ToolApprovalResult {
  request_id
  call_id
  decision
}

ToolApprovalDecision = approved | denied | timed_out
```

Approval events are semantic records. The user interaction itself may be
implemented through a UI/Gateway capability, callback, terminal, or remote
approval system.

## Approval Rules

- Approval is evaluated against the validated effective arguments, not only the
  model's original argument object.
- A missing approval channel must not become implicit approval.
- Approval timeout or interaction failure must fail closed.
- Approval from an earlier call must not automatically authorize a different
  call unless the configured policy explicitly defines a safe reusable grant.
- A denied call returns `status=denied` and `side_effect=none`.
- Approval decisions are runtime or policy input, never model-written tool
  arguments.

The base contract does not prescribe one approval philosophy. Services may
require approval always, only for selected effects, or never under an explicitly
configured autonomous policy.

## Tool Backends

A backend may be:

- an in-process function
- another capability invoked through the mediator
- a local subprocess
- a plugin
- an MCP server
- a remote API
- a browser, VM, or sandbox service

The backend kind must not change the base catalog, call, and result promises.

Backend-specific identities, wire payloads, and connection details remain
private unless another capability has a concrete need for them.

An MCP server is treated as an external tool provider. Its schemas, names,
output, and availability receive the same validation as every other backend.

Some MCP servers can ask the client to call a model or ask the user a question
while a tool is running. Those callbacks are additional authority requests.
They must be separately enabled and bounded; they do not automatically inherit
all authority of the original tool call.

## Provider-Hosted Tools

Some model providers execute search, code, or other tools inside the model API
without returning a client-executable tool call.

Those effects did not pass through this capability. The harness must not record
them as though Tool Invocation approved or executed them.

For v0, a harness should either:

- disable provider-hosted tools, or
- represent them explicitly in Model Invocation events as provider-owned
  effects

A later contract may define a unified representation when concrete provider
behavior justifies it.

## Concurrency

- `sequential` calls execute in input order.
- `allow_parallel` lets the service choose safe concurrency.
- Model-provider support for parallel tool calls is not evidence of execution
  safety.
- Interactive calls, calls with shared mutable state, and calls with uncertain
  effect behavior should default to sequential execution.
- Path-based operations may execute concurrently only when their effective
  paths and effects do not conflict.
- Unknown plugin or MCP tools should default to sequential execution unless the
  service has an explicit reason to treat them as safe.
- Worker and backend concurrency must be bounded.
- Results preserve input order regardless of completion order.
- Calls that can produce a catalog transition are serialized within an
  execution batch and form a barrier against other catalog-changing calls for
  the same runtime catalog lineage.
- Several successful `tool_load` calls in one batch are applied in input-call
  order and produce at most one final catalog transition.
- In v0, a catalog lineage is identified by `(session_id, turn_id)`. Branching
  and concurrent continuations of the same session turn are not supported.
- Tool Invocation must serialize catalog-changing calls for the same v0
  lineage. The Runtime Kernel must serialize catalog adoption for that lineage.
- Before adopting a transition, the Runtime Kernel must compare its active
  catalog ID with `base_catalog_id`. A mismatch is a catalog conflict: the
  transition must not replace the active catalog, and the runtime must refresh
  or restart the continuation from its current catalog.

## Cancellation And Timeouts

The service must honor the current mediator cancellation signal and request
deadline when the backend supports cancellation.

Cancellation and timeout describe the harness's waiting state. They do not
prove that a backend stopped or that no side effect occurred.

When a call was definitely prevented from starting:

```text
status = cancelled or timed_out
side_effect = none
```

When a started call was abandoned and its effect cannot be checked:

```text
status = cancelled or timed_out
side_effect = unknown
```

When a backend later provides reliable completion evidence before the terminal
batch is recorded, the service should return that real result rather than a
fabricated timeout.

Unstarted sibling calls in an interrupted batch must receive explicit cancelled
or timed-out results when the service can produce a complete terminal batch.
They must not disappear.

## Idempotency, Retry, And Replay

`tool_invocation.execute` requires an `idempotency_key`.

Within the active service's duplicate-protection scope:

- repeating the same key and same payload must return the recorded result,
  current known result, or an explicit unknown outcome
- it must not silently execute the calls again
- repeating the same key with a different payload must fail as an idempotency
  conflict without executing the new payload

When `session_id` is present, the duplicate-protection key is scoped to that
session. Otherwise it is scoped to the configured service instance. The service
must retain enough information to prevent duplicate execution until the
terminal execution event is durable. A richer service may advertise a longer
retention window.

The service should pass a stable call identity or backend idempotency key to
effectful backends that support one.

No contract can guarantee exactly-once effects when a remote backend loses its
response after applying a change. The correct outcome in that case is
`side_effect=unknown`, not an automatic retry.

Semantic event replay must never execute tools. Replay reads the recorded model
input evidence and execution events.

Primary/shadow execution requires special care:

- the primary Tool Invocation service may commit live effects
- a shadow service must not execute live effects unless it advertises and
  receives an isolated dry-run or sandbox mode
- comparison must not duplicate user-visible or external changes

## Result Handling

Tool Invocation is responsible for the result that crosses the capability
boundary.

It must:

- validate that the backend returned a supported result
- produce non-empty model-facing `text`
- sanitize secrets, control framing, and unsafe backend error text
- enforce configured result-size limits
- create refs for large or non-text material when supported
- report artifact-persistence failure explicitly
- avoid claiming that truncated output is complete

Tool output may contain prompt injection or misleading instructions. Result
text is untrusted model input even when it came from an installed plugin or MCP
server.

The base contract uses one canonical text field plus refs. It does not expose
raw backend output, arbitrary UI details, or a generic metadata map.

## Session Interaction

`ToolResult` is not a `SessionRecord`.

The runtime may append a session record using:

- `ToolResult.text`
- `ToolResult.refs`
- `kind=tool_result`

Structured status, failure, side-effect, and touched-path evidence remain in
Tool Invocation events unless a later Session contract establishes a concrete
need to preserve them directly.

Tool calls and results may be persisted incrementally during an active turn.
The runtime should record accepted call intent before allowing an effectful
backend to start, then record its outcome when known.

Merely receiving `session_id` does not grant Tool Invocation or a backend access
to the session record. A session-reading tool must invoke Session through the
mediator with explicit authority.

## Context Provider Interaction

After execution, the runtime may call:

```text
context_provider.get_context(
  reason=tool_result,
  touched_paths=ToolResult.touched_paths
)
```

Touched paths are evidence for context discovery. They are not filesystem
grants and do not require Session to persist them first.

## Model Invocation Interaction

Tool Invocation supplies canonical `ToolDefinition` values.

Model Invocation:

- encodes definitions for the selected model provider
- accumulates streamed tool-call fragments
- maps provider-specific names back to canonical names
- produces complete normalized `ToolCall` values
- reports malformed or incomplete provider output to the Runtime Kernel

Tool Invocation:

- resolves the canonical tool identity and definition revision against its
  current registration
- validates arguments against the matched canonical schema
- applies current caller authorization when configured and current tool policy
- executes the call

Neither capability executes the other's responsibilities.

After a tool-execution terminal event is recorded, the Runtime Kernel processes
any `catalog_transition` before starting another Model Invocation. When
`base_catalog_id` still matches the runtime's active catalog, the runtime swaps
the active reference to `catalog_transition.catalog` and supplies that complete
catalog separately from the conversation and materialized context. Model
Invocation then encodes the new catalog through the provider's native tool
mechanism.

This transition is ordinary typed runtime control flow, not model-facing text,
an arbitrary event hook, or permission for Model Invocation to construct a
catalog. The terminal execution event is the canonical record of both the
model-facing tool results and the catalog transition.

## Security Rules

- Model-written arguments are untrusted input.
- Caller identity, approval, and sandbox selection must not be accepted from
  model arguments.
- Current caller authorization must be checked at execution time when it is
  configured, not only at catalog time.
- Tool and schema names from plugins or remote servers must not silently
  replace existing tools.
- Tool descriptions and schemas from remote sources must be bounded and
  validated before becoming model input.
- Tool results and backend errors must be treated as untrusted content.
- Secrets must not be copied into model-facing text, approval summaries, or
  ordinary event fields.
- Tool Invocation must not bypass or broaden the runtime-supplied execution
  boundary. Backend connections must not retain revoked caller authorization or
  credentials from earlier requests.
- Approval failure must fail closed.
- An unknown effect outcome must not be rewritten as `side_effect=none`.
- A backend must not gain access to other capabilities merely because it was
  reached through Tool Invocation.

## Side Effects

Tool Invocation may perform any effect explicitly allowed by:

- the selected tool
- the current runtime-supplied execution boundary
- service policy
- required approval

It must not:

- execute a call unless it resolves to a currently registered effective tool
- execute denied or invalid calls
- bypass or broaden the runtime-supplied execution boundary
- hide a known partial effect behind a generic failure
- report uncertain effects as absent
- rerun a recorded call during replay

## Unsupported Requests

Unsupported actions return the project-wide `capability.unsupported` terminal
event.

Example:

```text
command: tool_invocation.install_backend
terminal event: capability.unsupported
```

Tool installation, backend administration, and policy editing are not part of
the v0 runtime contract.

## Invariants

- Every catalog has a stable `id`.
- Every tool definition has a stable canonical `id`.
- Every exact tool definition has a content-derived `revision`.
- Tool names are unique within a catalog.
- Catalog output is a complete current snapshot.
- Every model-produced execution request identifies the catalog supplied to
  that model invocation.
- A catalog transition identifies its immutable base and contains one complete
  replacement catalog.
- The runtime adopts a catalog transition only when its active catalog still
  matches the transition's base.
- A catalog never grants permanent execution authority.
- Model-written arguments never expand caller authorization or select a broader
  execution boundary.
- Every accepted complete execution returns one ordered `ToolResult` per call.
- No call is silently dropped.
- A known call result is not discarded because a sibling call failed.
- Denied and invalid calls do not start a backend.
- `call_started` is acknowledged as recorded before a backend may perform a
  side effect.
- Result order matches call order.
- `status` and `side_effect` are reported independently.
- Cancellation and timeout do not imply `side_effect=none`.
- Duplicate idempotent execution requests do not silently rerun effects.
- Replay never executes tools.
- Shadow evaluation never duplicates live effects.
- Session text is not the only record of structured tool outcome.
- Current caller authorization and the runtime-supplied execution boundary
  override older catalog, cache, and backend state.

## Failure Semantics

Expected request-level failures include:

- invalid or empty request
- missing idempotency key
- duplicate call IDs
- idempotency conflict
- service unavailable
- internal failure before complete results can be produced

Expected call-level failures include:

- unknown tool
- invalid arguments
- tool unavailable
- policy or approval denial
- access-scope violation
- backend failure
- malformed result
- result too large
- timeout
- cancellation
- unknown remote outcome

Request validation that rejects the entire batch must happen before any call
starts.

Once execution begins, the service should return as much known per-call outcome
as possible. If it cannot produce one result for every call, it returns
`tool_invocation.execution_failed` with known results and unresolved call IDs.

## Lifecycle

The base service lifecycle is:

```text
configured
  -> backends discovered
  -> catalogs listed many times
  -> execution requests accepted many times
  -> service stopped
```

Catalogs may change as configuration, credentials, authorization, policy,
plugins, MCP servers, or definition validity change. Transient backend health
does not by itself change a catalog.

Stopping the service must:

- stop accepting new execution requests
- request cancellation of in-flight work
- preserve known outcomes
- mark unresolved outcomes honestly
- close backend connections and subprocesses when possible

The base contract does not define backend installation, policy editing, or
service configuration actions.

## Minimal Test Fixtures

A service implementing `tool_invocation.v0` should be testable with:

- list an empty valid catalog
- list several tools with stable IDs, unique names, descriptions, and
  object-shaped input schemas and content-derived definition revisions
- return a complete catalog snapshot rather than a delta
- reject or explicitly resolve duplicate tool names
- omit a tool that is no longer publication-eligible from a new catalog
- keep an eligible tool and its catalog ID stable across a transient backend
  outage, then return `tool_unavailable` if it is called while unavailable
- keep `tool_search` and `tool_describe` catalog-neutral when progressive
  disclosure is enabled
- load one discoverable tool from a known base catalog and return one complete
  catalog transition with the tool appended
- apply several successful tool loads in call order and return one final
  catalog transition
- omit a transition when a requested tool is already present with its current
  definition
- fail a load whose base catalog cannot be resolved without changing the
  active catalog
- evict a base from a bounded snapshot cache and return `catalog_unavailable`
  for a later load against that ID
- reject a catalog-changing request without both session and turn identity
  before any call starts
- serialize catalog-changing requests sharing one `(session_id, turn_id)`
- reject adoption of a transition whose base no longer matches the runtime's
  active catalog
- record generic proxy dispatch as an attempt against its effective target when
  proxy dispatch is enabled
- execute one valid read-only call and return `succeeded + none`
- execute one valid mutating call and return an honest side-effect value
- reject an unknown tool with `failed + none`
- reject a stale tool-definition revision without starting the backend
- reject invalid arguments without starting the backend
- dispatch every backend through the runtime-supplied bounded execution route
- surface sandbox or capability access denial without retrying outside the
  supplied boundary
- refuse to provision or broaden a sandbox for a tool call
- deny a call by policy without starting its backend
- fail closed when required approval cannot be obtained
- execute a sequential batch in input order
- accept `allow_parallel` while retaining the right to serialize unsafe calls
- preserve result order for a safely parallel batch
- return one result for every call in a completed batch
- preserve known sibling results in a request-level failure
- return cancelled outcomes for calls that did not start after interruption
- return `timed_out + unknown` when a started effectful backend is abandoned
  without reliable completion evidence
- prefer a reliable late completion result over a fabricated timeout when it
  arrives before the terminal batch is recorded
- repeat an execution request with the same idempotency key without rerunning
  its backend
- reject reuse of an idempotency key with a different payload
- return model-facing text with secrets and unsafe error framing removed
- represent large or non-text output through useful text and `ContextRef`
  values
- return `TouchedPath` evidence for known file operations
- request a runtime stop without directly owning the stop decision
- route an MCP-backed tool through the same catalog, validation, and result
  rules as a built-in tool
- refuse to execute recorded calls during semantic replay
- refuse live side effects in shadow mode without an isolated dry-run or
  sandbox
