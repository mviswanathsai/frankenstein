# Tool Invocation Service Dossier

Date: 2026-08-01

Status: working capability analysis.

Contract draft: `docs/tool-invocation-capability-contract.md`.

This dossier records the evidence and boundary decisions behind the Tool
Invocation capability. It is descriptive working material. The contract is the
stable surface.

## Working Conclusion

Tool Invocation is the harness boundary that turns model-written tool requests
into controlled, recorded tool outcomes.

It has two related jobs:

1. publish the tools that the model may currently request
2. validate, authorize, execute, and normalize those requests

The capability aggregates tools from built-in handlers, plugins, other
capabilities, MCP servers, and remote backends. Those sources are implementation
details. The runtime sees one catalog and one execution surface.

Tool Invocation owns tool-execution state. It does not own the whole agent
loop, the canonical session record, or the domain state behind every tool.

## Capability

Tool Invocation.

## User-Visible Job

Let the model use tools safely and predictably.

From a user's point of view, this includes:

- showing the model the right tools
- rejecting calls the model is not allowed to make
- asking for approval when required
- preventing unsafe concurrent execution
- reporting progress for long-running work
- returning useful results and useful errors
- avoiding duplicate side effects during retry or recovery
- preserving enough evidence to explain what happened

Swapping this capability can substantially change the harness's safety,
autonomy, approval, validation, and tool-ecosystem philosophy.

## Runtime Job

The capability:

- builds a current model-facing tool catalog
- assigns stable canonical identities to tools
- rejects name collisions or resolves them explicitly
- validates tool names and arguments
- applies configured policy and current runtime authority
- obtains approval when policy requires it
- dispatches calls to the correct backend or capability
- chooses safe sequential or parallel execution
- propagates cancellation and deadlines
- bounds and normalizes results
- records whether side effects are known to have happened
- returns one outcome for every accepted call

The runtime kernel remains responsible for deciding when tools are requested,
when the model is called again, and when the overall turn ends.

## Concrete Hermes Evidence

### Registry And Catalog Assembly

Relevant files:

- `tools/registry.py`
- `model_tools.py`
- `toolsets.py`
- plugin tool registration
- `tools/mcp_tool.py`

Observed behavior:

- tools self-register schemas, handlers, toolset membership, availability
  checks, descriptions, and result limits
- catalog assembly filters enabled and disabled toolsets
- some schemas are rebuilt from current runtime configuration
- unavailable dependencies remove tools from the current catalog
- MCP servers can add and refresh tools dynamically
- registration detects name collisions and requires deliberate override for
  protected built-in tools
- large catalogs may be collapsed behind tool-search tools
- schemas are sanitized for model/provider compatibility
- Hermes snapshots the selected model-visible tools when an agent is built and
  sends that same snapshot on every model call in the tool loop.
- When MCP tools are registered, Hermes re-derives the snapshot between user
  turns, before the next turn's first model call. Explicit MCP reload paths can
  also rebuild it.
- Progressive tool disclosure does not expand the provider-visible tools array
  mid-turn. The model sees `tool_search`, `tool_describe`, and `tool_call`; each
  bridge invocation rebuilds the scoped hidden catalog from the current
  registry and searches, describes, or dispatches through that indirection.

Hidden coupling:

- the set shown to the model depends on configuration, installed dependencies,
  credentials, backend health, current toolsets, and context-window pressure
- process-global registration makes it easy for one session's tool choices to
  leak into another unless catalog construction is explicitly scoped

### Call Parsing And Validation

Relevant files:

- `agent/tool_executor.py`
- `model_tools.py`
- `tools/schema_sanitizer.py`
- `tools/tool_search.py`

Observed behavior:

- malformed JSON arguments become tool errors instead of being executed
- unknown tools are rejected
- Hermes performs limited schema-guided coercion for common model mistakes
- deferred tool calls are resolved to their underlying canonical tool before
  policy hooks and execution
- the underlying tool must still belong to the current session's allowed
  catalog
- errors returned to the model are length-bounded and stripped of framing that
  could confuse later model output

Hidden coupling:

- repair behavior is a policy choice, not merely parsing
- a model-facing alias or deferred call must not bypass authorization on the
  underlying tool

### Policy, Approval, And Guardrails

Relevant files:

- `tools/approval.py`
- `hermes_cli/plugins.py`
- checkpoint helpers
- guardrail paths in `agent/tool_executor.py`

Observed behavior:

- policy can allow, deny, or require user approval
- approval behavior depends on whether the caller is interactive, gateway,
  cron, or another runtime surface
- missing approval listeners and approval timeouts must fail closed
- plugin middleware can inspect, replace, block, or approve arguments
- destructive commands and sensitive paths receive additional checks
- checkpoint and file-mutation verification protect or verify selected effects

Hidden coupling:

- current session identity, surface callbacks, policy configuration, and
  runtime mode influence approval
- model-written arguments cannot be trusted to identify the current user,
  session, workspace, or authority

### Execution And Concurrency

Relevant files:

- `agent/tool_executor.py`
- `agent/tool_dispatch_helpers.py`
- `model_tools.py`

Observed behavior:

- batches may execute sequentially or concurrently
- interactive tools such as `clarify` must not run concurrently
- file operations can run concurrently only when their target paths do not
  overlap
- unknown, plugin, and MCP tools default to the safer execution mode unless
  explicitly marked safe
- worker limits prevent unbounded parallelism
- cancellation must be propagated into worker threads and async tasks
- a timeout may abandon work that is still running

Hidden coupling:

- provider output containing several tool calls is not proof that those calls
  are safe to execute concurrently
- a timeout or cancellation does not prove that a remote or abandoned tool
  produced no side effect

### Runtime-Bound Tools

Relevant files:

- `agent/tool_executor.py`
- `model_tools.py`
- context-engine and memory-manager integrations

Observed runtime-bound tools include:

- `todo`, which uses the active todo store
- `session_search`, which uses the current session database and session
  identity
- `memory`, which uses the active memory store and may notify an external
  memory provider
- `clarify`, which needs the current user-interaction callback
- `read_terminal`, which needs the current terminal or UI buffer
- `delegate_task`, which needs the active subagent runtime
- context-engine tools, which use the active compressor and current messages
- memory-provider tools, which use the active provider manager

The lesson is not that Tool Invocation should own all of this state. These tools
are model-facing adapters over runtime or capability operations:

```text
memory tool       -> Memory capability or configured memory backend
session tool      -> Session capability
clarify tool      -> UI or Gateway capability
delegate tool     -> Runtime or Scheduler
browser tool      -> Browser backend
MCP tool          -> MCP server
```

The tool service receives scoped runtime context and routes the call to the
state owner.

### Result Handling

Relevant files:

- `agent/tool_dispatch_helpers.py`
- `agent/tool_result_classification.py`
- `tools/tool_result_storage.py`
- session persistence paths

Observed behavior:

- tool results may be text, structured data, images, or artifact-like payloads
- large results are truncated or persisted outside the immediate model context
- untrusted web/browser output is marked and framed as untrusted
- known file-write outcomes are distinguished from uncertain outcomes
- every model tool-call ID needs a corresponding tool result for replay
- results are kept in model-call order even when execution happens in parallel
- runtime callbacks receive start, progress, completion, and error information

Hidden coupling:

- model-facing content, UI details, stored artifacts, and audit evidence are
  different consumers of the same execution
- raw backend errors may contain secrets or adversarial text
- session records preserve normalized text and refs, while structured tool
  outcomes belong to Tool Invocation events

### MCP Backends

Relevant file:

- `tools/mcp_tool.py`

Observed behavior:

- MCP tools are discovered and registered alongside built-in tools
- stdio, HTTP, and SSE transports have different lifecycle and failure modes
- servers may disconnect, refresh their tool list, or change schemas
- tool names from different servers may collide
- server output and error messages require sanitization
- per-server timeout and parallelism choices affect execution
- some MCP servers can request model calls or user input from the client,
  creating an additional authority boundary

An MCP server is therefore one tool backend. It is not automatically the
harness-wide Tool Invocation service.

## Pi Evidence

Relevant files:

- `packages/agent/src/agent-loop.ts`
- `packages/agent/src/types.ts`
- `packages/ai/src/utils/validation.ts`

Observed behavior:

- the model adapter returns normalized tool-call blocks
- the agent loop finds tools and validates arguments before execution
- tools receive cancellation and progress callbacks
- tools can opt into sequential or parallel execution
- parallel execution still preserves result order
- a tool may request that the runtime stop after the current batch
- calls from an output-token-truncated model response are not executed, even
  when repaired arguments appear valid

This supports a boundary where Model Invocation decodes calls, Tool Invocation
executes them, and the Runtime Kernel controls the loop.

## State Owned Or Mutated

A Tool Invocation service may own:

- canonical catalog construction and immutable snapshots needed by active model
  calls
- canonical tool identities and model-facing names
- backend registrations and connections
- backend availability observations
- tool-specific policy configuration
- pending approval state
- in-flight invocation state
- concurrency locks and worker limits
- checkpoints used specifically for tool execution
- idempotency and duplicate-detection records
- bounded result artifacts that it creates

It does not own:

- the canonical session transcript
- the complete live agent turn
- model retry or fallback state
- global turn budgets
- the memory database merely because it exposes a memory tool
- the browser's domain state when a browser service owns it
- provider-native model tool-call formatting

## Inputs

Observed inputs include:

- the current tool catalog request
- normalized model tool calls
- the exact catalog ID used by the model invocation that produced those calls
- current session and turn references
- caller-resolved working directory
- the runtime-supplied bounded execution route or environment
- configured tool policy
- cancellation and deadline signals
- user approval responses
- tool-specific runtime or capability access

Model-written arguments and runtime-supplied authority are separate inputs. The
model must not be able to choose its session identity, filesystem grants,
approval state, tenant, or caller authority through ordinary tool arguments.

## Outputs

Observed outputs include:

- a complete current tool catalog
- an optional complete catalog transition after successful tool loads
- one normalized result for every accepted call
- sanitized model-facing text
- refs to artifacts, files, URLs, or other output material
- touched-path evidence
- explicit success, failure, denial, cancellation, timeout, or unknown outcome
- explicit knowledge about whether side effects occurred
- optional stop requests for the runtime
- start, progress, approval, and completion events

## External Effects

Depending on configured tools, execution may:

- read or write files
- execute commands
- use browser or VM sessions
- make network requests
- mutate memory or session state through their owning capabilities
- ask the user for approval or clarification
- start child agents or background work
- invoke remote MCP servers
- create artifacts

The Tool Invocation contract must describe how these effects are authorized and
reported without prescribing how every backend implements them.

## Failure Modes

Expected failures include:

- catalog construction unavailable
- active base catalog unavailable for a catalog-changing call
- duplicate or ambiguous tool names
- unknown tool
- malformed or non-object arguments
- schema validation failure
- tool unavailable after publication
- policy denial
- approval denial or timeout
- sandbox or access-scope violation
- backend unavailable
- backend disconnect
- tool timeout
- cancellation
- worker or process crash
- malformed backend result
- result too large
- artifact persistence failure
- uncertain side effect after timeout or lost connection
- duplicate execution request

## Recovery Behavior

Required recovery principles:

- invalid and denied calls produce explicit non-executed outcomes
- one failed call does not erase known outcomes for sibling calls
- unstarted calls receive explicit outcomes when a batch stops early
- duplicate requests with the same idempotency key do not silently rerun
  side effects
- uncertain remote outcomes remain unknown rather than being reported as
  failures with no effect
- replay reads recorded outcomes and never re-executes tools
- raw backend errors are sanitized before becoming model-visible

## Hidden Coupling To Avoid

- making the tool service own the whole mutable agent object
- allowing model arguments to carry runtime authority
- using provider-native tool names or IDs as canonical identities
- letting tool registration overwrite another tool silently
- treating a published catalog as permanent execution authority
- assuming cancellation means no side effect
- persisting only a final turn and losing evidence around a side effect
- treating MCP tools as more trusted than built-in or plugin tools
- storing structured tool payloads only in generic session text

## Possible Alternate Philosophies

- strict schema-only executor with no argument repair
- compatibility executor with limited, recorded coercion
- human approval for every effectful call
- capability-scoped broker that exposes only a few tools per turn
- large dynamic catalog with tool search
- sequential-only executor
- effect-aware parallel executor
- local in-process registry
- remote MCP-heavy broker
- dry-run or shadow evaluator that never commits effects

These choices are meaningful enough that Tool Invocation deserves a capability
contract.

## Reconciliation With Existing Contracts

| Need | Existing owner and shape | Reuse decision | Remaining gap |
|---|---|---|---|
| Session identity | Session `id`, referenced as `session_id` | Reuse | None |
| Turn identity | Runtime/command causality | Reference as `turn_id` | Runtime contract is not drafted yet |
| Working directory | Context Provider `RuntimeFacts.cwd` | Preserve it only as location | The runtime-supplied execution boundary owns access authority |
| Filesystem roots | Context Provider `WorkspaceRoot` | Do not reuse as Tool Invocation authority | Sandbox or execution-boundary contracts own access semantics |
| Result references | Session `ContextRef` | Reuse | None |
| Touched-path evidence | Context Provider `TouchedPath` | Reuse | Tool Invocation becomes a producer of this evidence |
| Session history text | Session `SessionRecord` | Do not use as the structured tool result | Runtime may append a normalized text/ref projection |
| Live turn state | Runtime Kernel | Do not copy into Tool Invocation | Pass only needed runtime context |
| Model-facing call | No shared base shape yet | Define `ToolCall` here | Model Invocation must produce this shape later |
| Tool outcome | No shared base shape yet | Define `ToolResult` here | Session and Model Invocation consume projections |
| Active catalog replacement | Tool Invocation `ToolCatalogTransition` | Carry it on the execution terminal result | Runtime compare-and-swaps the active reference before Model Invocation |

## Substitution Test

Contract-worthy: yes.

Reasons:

- safety and autonomy policy changes user-visible behavior
- multiple plausible implementations already exist
- tool ecosystems and backends vary substantially
- the rest of the harness can depend on a small catalog-and-execute surface
- real failure modes require explicit cross-boundary representation

## Contract Requirements Derived From The Evidence

The first contract must:

- expose complete catalog snapshots rather than registration internals
- identify the exact catalog used by every model-produced execution request
- carry a typed complete catalog transition separately from model-facing result
  text when a successful tool load changes the next model input
- keep canonical tool identity separate from model-facing name
- separate model arguments from runtime authority
- execute a batch while returning results in call order
- let the service choose whether allowed parallelism is actually safe
- require duplicate protection for execution requests
- distinguish call outcome from side-effect certainty
- return one result per call whenever the service can produce a terminal batch
- preserve known partial outcomes when the service cannot finish the batch
- reuse `ContextRef` and `TouchedPath`
- make approval, cancellation, timeout, and replay behavior explicit
- avoid a generic `agent_state`, `metadata`, or raw backend payload

## Discussion Record

This section preserves design and implementation discussion across working
sessions. It is not normative. Accepted entries are directions for later
contract reconciliation, not changes to the current draft by themselves.

### Accepted Directions

#### Provider publication and catalog construction

- A tool provider should publish the facts Tool Invocation needs to register
  and operate a model-facing tool. Publication does not grant authority or
  guarantee catalog inclusion.
- Provider facts and invocator decisions must stay separate. Providers describe
  the tool; Tool Invocation owns enablement, collision handling, catalog
  inclusion, policy checks, approval, routing, and conservative execution
  defaults.
- A shared provider-to-invocator publication surface will be needed before
  independently implemented capability services can publish tools portably.
  The model-facing `ToolDefinition` should not carry backend routing, policy,
  or authority details.
- That future publication surface must distinguish publication eligibility
  from runtime availability. Eligibility decides whether a registered tool may
  appear in a catalog; availability is transient backend liveness checked at
  execution. No current shared provider-publication type expresses both facts,
  and one `available` field must not be made to carry both meanings.
- Tool definitions are part of the structured model input but are not
  transcript records. Model Invocation receives the selected canonical catalog
  separately from the materialized context and transcript, then encodes those
  inputs for the selected model provider.
- Tool Invocation is the sole producer of canonical catalogs. The runtime owns
  only the active catalog reference used to sequence a model call; it may
  request, retain, reuse, and route a catalog but must not assemble or modify
  one. Model Invocation owns only provider encoding.
- The selected catalog and materialized context must both exist before Model
  Invocation. Tool Invocation must precede Context Construction only when
  Context Construction selects tools or accounts for their token budget;
  otherwise the runtime may obtain the two inputs independently.
- Tool Invocation may publish model-facing `tool_search`, `tool_describe`, and
  `tool_load` as ordinary tools in the visible catalog. The discoverable
  registry is distinct from the provider-visible catalog.
- Discovery must expose only tools reachable in the current request scope and
  does not grant execution authority.
- Search and describe are catalog-neutral. `tool_load` is the model's explicit
  request to make a discoverable target directly callable. Description is the
  expected decision aid but is not a required predecessor to loading.
- A successful load returns an ordinary model-facing result and, at the batch
  level, a typed catalog transition containing its immutable base catalog ID
  and one complete replacement catalog. The transition must not be hidden in
  model-facing result text or an arbitrary hook flag.
- The runtime processes the typed transition through its normal post-execution
  path before the next Model Invocation. It compares the transition base with
  its active catalog reference, adopts the replacement on a match, and passes
  that catalog separately to Model Invocation for provider encoding.

#### Identity and catalog persistence

- Canonical tool identity should use
  `<service-provider>:<service-provider-version>:<provider-local-tool-name>`.
  This identity is internal to the canonical catalog and is not the name sent
  to the model. Upgrading the service provider deliberately changes its tool
  identities.
- The provider-local tool name is distinct from any alias or encoding required
  by a model provider. Model Invocation converts the canonical catalog to the
  selected provider's wire format and maps returned calls back to canonical
  identities.
- Catalog identity should work across restarts without a stateful integer
  allocator. A content-derived ID over the exact ordered canonical catalog is
  the current preferred implementation direction.
- Repeated identical catalogs should be deduplicated in durable storage. Events
  refer to the catalog ID; a retained catalog is garbage-collectable only when
  no retained event depends on it for execution recovery or replay.
- The canonical catalog alone does not prove exactly what was sent to a model
  provider. Model Invocation must retain the provider-encoded tool bundle, or a
  durable reference to it, when exact model-input replay is required.
- Catalogs are immutable, content-addressed model-input evidence. Store each
  canonical body once, let every Model Invocation event reference the catalog
  it used, and derive a session's catalog timeline and union of tools ever shown
  from those ordered events.
- Tool Invocation does not own historical catalog retention or resolve
  execution through historical catalogs. Model Invocation attaches canonical
  tool identity and definition revision to a normalized call; Tool Invocation
  resolves those values against its current registration.
- Catalog-changing calls create a narrower retention requirement: Tool
  Invocation keeps a bounded in-memory cache of immutable catalog snapshots
  keyed by catalog ID. The cache is not a mutable global current catalog and
  does not own the runtime's active reference.
- Catalog construction, including construction after `tool_load`, produces a
  complete snapshot from current registry, authorization, policy, and
  publication eligibility. Transient backend availability does not change
  membership or catalog identity. Cached snapshots supply only the immutable
  membership and ordering base needed by a catalog-changing call.
- Cache capacity and eviction are implementation choices. A missing base
  produces `catalog_unavailable`; the runtime obtains a fresh catalog for a
  later model invocation. Tool Invocation does not need durable or unbounded
  historical catalog storage.
- `ToolExecutionRequest.catalog_id` identifies the catalog that produced the
  model calls. It is runtime-supplied evidence and a catalog-transition base,
  not model-written authority.
- In v0, `(session_id, turn_id)` identifies the only catalog lineage. Tool
  Invocation serializes catalog-changing calls for that pair, the runtime
  serializes adoption, and concurrent continuations or branches of one turn are
  unsupported.

#### Sandboxing and optional RBAC

- For an initial implementation, resource authority should be enforced by the
  execution sandbox rather than by a general Tool Invocation scope algebra.
  This is sound only when the sandbox bounds every relevant channel, including
  filesystem, process, network, credentials, and capability or mediator
  handles.
- Tool Invocation does not provision, configure, or own sandboxes. The runtime
  supplies an already-bounded execution route or environment, and Tool
  Invocation dispatches the selected backend through that boundary.
- Caller authorization is distinct from resource containment. In a multi-user
  harness, Tool Invocation is the natural RBAC enforcement point because it
  receives the authenticated caller and canonical tool identity during both
  catalog construction and execution.
- Tool Invocation need not own identities, roles, memberships, tenants, or
  policy merely because it enforces an authorization decision. Caller identity
  must be runtime- or mediator-supplied and never model-written.
- The smallest prospective RBAC rule is authorization of a principal to execute
  a canonical tool ID. Catalog construction omits unauthorized tools and
  execution checks again because authorization may have changed after catalog
  publication.

#### Side-effect evidence

- The mediator must acknowledge that `tool_invocation.call_started` was
  recorded before an effectful backend starts. Tool Invocation does not own the
  event store; it waits for the mediator's acknowledgement and does not dispatch
  the backend when the record cannot be acknowledged.

### Tentative Implementation Choices

- Use the canonical tool ID directly as the initial RBAC resource instead of
  defining provider-published permission scopes.
- Treat the registered tool set, a catalog, and the model-provider tool bundle
  as three different values. The registry is current invocator state; a catalog
  is an immutable request-specific projection; the provider bundle is Model
  Invocation's encoding of that projection.
- Build every requested catalog as a complete current projection. Do not add a
  mutable per-user catalog cache. The bounded ID-keyed snapshot cache exists
  only to resolve immutable transition bases; it is not a cached authorization
  or catalog-selection decision.
- Permit another complete catalog snapshot for a later model invocation when
  relevance changes. Dynamic tool selection should not mutate the catalog that
  an earlier model invocation used.
- Permit the runtime to reuse an earlier catalog snapshot when no new catalog
  request is needed. Every `list_tools` request constructs a current complete
  snapshot, but neither Context Construction nor Tool Invocation must run again
  before every model call when their respective inputs remain valid.
- Treat a catalog as a reusable structured segment of model invocation input,
  not as text embedded in the system prompt or transcript. Context Construction
  may consume or reference it for selection and budgeting without owning the
  catalog artifact. It may remain active across model calls and user turns until
  invalidated. A turn-boundary freshness check is not itself invalidation when
  it returns the same catalog.
- Prefer stable `tool_search`, `tool_describe`, and `tool_load` definitions when
  progressive disclosure is enabled. An ordinary description result can teach
  the model a schema but does not make the target provider-natively callable.
  In the direct strategy, a later successful `tool_load` produces the catalog
  transition that Model Invocation encodes through native late loading or a
  replacement provider tool bundle.
- A provider may represent late loading as an appended tool-search output or
  tool reference rather than a replacement request prefix. Use that native
  representation when available: it preserves direct calls and may preserve
  provider-side prompt-cache reuse. Otherwise, expanding the next provider tool
  bundle creates a new input generation and may lose cache reuse at or after
  the tools segment.
- Treat durable catalog storage as an observability or recovery requirement,
  not as proof that Tool Invocation must own a catalog database. A bounded
  in-memory snapshot cache is sufficient for v0.
- Keep identical catalog content independent of the requesting principal: two
  callers who receive the same ordered definitions may share one content-derived
  catalog ID, while current authorization is still checked at execution.
- Treat temporary backend unavailability as an execution-time condition. Keep
  an otherwise eligible tool discoverable and catalogued, then return
  `tool_unavailable` when a call cannot currently be dispatched. This avoids
  catalog churn without weakening execution-time checks.
- Do not require a generic execution proxy in the first implementation. It is a
  compatibility strategy for providers without late-bound tool publication, or
  for an implementation that deliberately chooses one stable generic call
  surface. If added later, the effective registered target must be first-class
  event evidence and all validation, authorization, policy, approval,
  concurrency, runtime-supplied execution containment, result handling, and
  side-effect reporting rules apply to that target.
- When proxy dispatch is enabled, emit first-class
  `tool_invocation.proxy_dispatch_attempted` evidence identifying the
  model-visible proxy, requested target name, and resolved effective target
  when known. All later execution and result evidence identifies the effective
  target.
- Direct disclosure and proxy dispatch may be implemented as experiment modes
  behind the same capability contract. Compare them with eager direct
  publication as a control. The direct flow includes the explicit `tool_load`
  model round trip; the proxy flow proceeds from description to proxy call.
  Use matched tasks and report results separately by provider/model and
  cold/warm cache state. At minimum record provider-reported cached input,
  changed/common-prefix tokens, model round trips, describe-to-effective-call
  latency, total latency, input/output tokens, task success,
  argument-validation failures, process CPU time, and peak resident memory.
- A future branching runtime must give each branch an independent active
  catalog head. A transition that conflicts with one head is not globally
  stale and may still be valid relative to another branch derived from its
  base. Do not add branch identity or multi-head adoption to v0.
- Store compact decisions here. If implementation sketches become substantial,
  move them to a linked tool-invocation implementation-notes document rather
  than expanding the capability contract.

### Open Questions

- Does `service-provider` identify an implementation type or a stable configured
  binding? Two configured instances of the same provider and version otherwise
  produce colliding tool IDs.
- What grammar and escaping rules apply to the three tool-ID components?
- What canonical serialization and digest format define a content-derived
  catalog ID?
- What exact Model Invocation event or artifact records the provider-encoded
  tool bundle?
- If Context Construction does not receive the catalog, which component owns
  the global input-token budget across materialized context and tool schemas?
- If RBAC becomes concrete, does the mediator provide a final allow/deny
  decision, or does Tool Invocation call a separate policy decision service?
- When delegated operation matters, how are the calling service (`actor`) and
  represented user (`subject`) expressed in shared command metadata?
- Is model-facing discovery required by the base capability or an optional
  advertised Tool Invocation strategy?
