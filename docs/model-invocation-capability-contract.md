# Model Invocation Capability Contract

Date: 2026-08-25

Contract version: `model_invocation.v0.1`.

Status: draft.

This document defines what the rest of the harness may expect from a Model
Invocation service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning live in
`docs/model-invocation-service-dossier.md`. This contract closes the
tool-invocation interaction: canonical catalog to provider encoding,
normalized tool calls, and repair evidence.

## Summary Of v0.1 Changes

- The success payload is the assistant turn itself, not a response object.
  Call provenance (`request_id`, `catalog_id`, `model`,
  `provider_response_id`) rides alongside the turn.
- Reasoning is typed `Evidence { kind, data }`. Intermediate layers carry it
  opaquely; each provider adapter owns whether evidence is encoded back,
  filtered out, or rejected.
- The input IR speaks of `turns`, matching the transcript-to-renderer-to-
  adapter dataflow, instead of `messages`.
- Generation parameters have a named home (`params`) starting with
  `max_output_tokens`; absent means service default.
- Streaming is a declared second-class observation channel. Observations
  are transport: they never enter the event log and carry no replay
  obligation. The terminal event alone is authoritative.
- The success-side decision vocabulary is exactly two fields:
  `tool_calls` and a typed `outcome` (`complete | max_output |
  content_filter`). Everything else on the payload is verbatim or advisory.
- Advisory channels exist for what nobody branches on: verbatim diagnostic
  `finish_reason` and open-keyed `labels`.
- Repair evidence (`repairs`) records what the model emitted versus what
  the service produced, including calls dropped to truncation. Every
  emitted call is structurally complete.

## Purpose

Model Invocation is the harness's single point of contact with model
providers.

It has one action: perform one model call against assembled provider-neutral
input plus a canonical tool catalog, and return the next assistant turn.

The service:

- encodes the input turns and catalog for the selected provider
- performs the call
- accumulates streamed fragments into a complete turn
- repairs malformed model output
- produces a normalized `AssistantTurn`

Streaming may be exposed as progressive observations, but observations are
transport. This contract defines no semantic stream events; the terminal
event is the record of the call.

The base flow, seen from this side, is:

```text
kernel -> tool_invocation.list_tools
kernel -> context_renderer.render produces ModelInput from the transcript
kernel -> model_invocation.invoke with input, catalog, route, and params
model invocation -> an AssistantTurn: content, evidence, tool_calls,
                    usage, provenance
kernel -> materializes the turn into the session transcript
kernel -> tool_invocation.execute with the returned tool_calls
kernel -> processes the tool-execution terminal event and any
          catalog_transition
kernel -> model_invocation.invoke again when continuing
```

The turn closes the dataflow loop: the session stores turns, the renderer
compiles stored turns into `ModelInput`, and this action returns the next
turn. An `AssistantTurn` projects onto an input `Turn` by dropping its
provenance fields; that projection is what materialization and later
re-rendering consume.

The first implementation may use direct in-process calls through the
mediator. Nothing in this contract requires a separate process or an event
bus.

## Boundary

Model Invocation owns the provider-facing encoding of one request, the
provider call itself, and the normalization and repair of the response.

It does not own:

- deciding what the model sees. The context renderer owns that; its render
  action produces the `ModelInput`.
- tool execution or authoritative tool resolution. Those belong to Tool
  Invocation.
- retry, backoff, fallback, or continuation decisions. Those belong to the
  Runtime Kernel, whose contract is not yet drafted.
- session record writes. Those belong to Session.
- turn lifecycle, budgets, or cancellation. Those belong to the Runtime
  Kernel.

Model Invocation consumes two artifacts produced elsewhere: the `ModelInput`
assembled by the context renderer and an immutable `ToolCatalog` from Tool
Invocation. It must not construct or modify either. It may encode both for a
provider, but it must not add, remove, or semantically change canonical
content.

### Reasoning Evidence Ownership

Reasoning evidence is attached to assistant turns and flows through the
whole pipeline: the session transcript carries it, the context renderer
carries it into `ModelInput`, and the provider adapter decides what reaches
the wire. Each layer has one job:

- intermediate layers (session, renderer, kernel) carry evidence opaquely.
  They never interpret it, transform it, or drop it silently.
- adapters own wire policy. An adapter that understands an evidence kind may
  encode it back to the provider. An adapter that does not may filter it
  out. An adapter whose provider cannot accept the supplied evidence fails
  the call rather than corrupting it.

There is no harness-wide requirement to echo evidence back. Whether a given
provider requires echo-back, tolerates it, or rejects it is adapter
knowledge, not harness knowledge.

## Required Actions

Each action is carried in the project-wide command envelope. The successful
output shown below is the payload of that action's success event. A direct-call
mediator may return the same terminal event to the caller.

### `model_invocation.invoke`

Perform one model call against assembled provider-neutral input and return
the resulting assistant turn.

Input:

```text
ModelInvocationRequest {
  id
  session_id?
  turn_id?
  model
  provider
  input: ModelInput
  catalog?: ToolCatalog
  params?: GenParams
}
```

`id` identifies this request. Terminal payloads refer to it as `request_id`.
It is an identity and correlation value, not a durable write idempotency
key.

`session_id` identifies the current session when known. It provides
correlation only. Merely receiving it does not grant session-record access.

`turn_id` identifies the current turn when known. It is assigned by the
session service, not by the caller. A request may carry it for correlation
when the session service has made it available; a missing `turn_id` is valid
when no turn is in progress or the turn identity is not yet known.

Correlation identifiers ride in the request payload in v0.1, following the
convention of the other contracts. Moving them to envelope-level causality
references is a recorded direction, not an obligation of this version.

`model` is required. It is the model identity to invoke.

`provider` is required. It identifies the provider to route this call to.
The Runtime Kernel supplies it alongside `model`; the service dispatches to
the configured adapter for that provider. Routing is a caller-owned
decision, which is what makes primary/shadow provider evaluation possible
without this service knowing about it.

`input` is required. It is the assembled provider-neutral model input. The
service encodes it for the selected provider; it does not take over the
renderer's layout decisions.

`catalog` is the `ToolCatalog` defined by `tool_invocation.v0`, reused by
name. It is absent for tool-less calls. When supplied, the service encodes
it for the selected provider without adding, removing, or semantically
changing definitions, preserves the catalog order when the selected provider
permits, and echoes the catalog identity as `catalog_id` on the success
payload.

`params` carries generation parameters:

```text
GenParams {
  max_output_tokens?
}
```

`max_output_tokens` is an integer output budget. Every absent field means
the service default for that parameter. New generation parameters join this
shape in later versions rather than growing the request ad hoc.

Successful terminal output:

```text
AssistantTurn {
  request_id
  content: string
  reasoning?: Evidence
  tool_calls: ToolCall[]
  outcome
  usage: CallUsage
  finish_reason?
  labels?
  catalog_id?
  model
  provider_response_id?
  repairs?: RepairNote[]
}
```

The payload pairs the assistant turn itself (`content`, `reasoning`,
`tool_calls`) with the provenance of the call that produced it. Provenance
fields do not project into the transcript.

`request_id` echoes `ModelInvocationRequest.id`.

`content` is the normalized assistant text. It is required and may be empty;
an empty string means the model produced no text.

`reasoning` is opaque reasoning evidence attached to this assistant turn:

```text
Evidence {
  kind
  data
}
```

`kind` names the evidence format (for example `text` for plain reasoning
summaries, or a provider-specific format identifier for structured or
cryptographically protected evidence). `data` is the evidence itself,
verbatim. Consumers must not interpret either field; adapters decide whether
an evidence kind can be encoded for their provider. See Reasoning Evidence
Ownership.

`tool_calls` is required and may be empty. Every emitted call is complete,
normalized, and uses canonical names. Model Invocation assigns each emitted
call's `id`, unique within the result. When a catalog was supplied and an
emitted name mapped to a definition in it, the call carries that
definition's `tool_id` and `definition_revision`; when the name did not
map, they are absent together. They are not model-written arguments.

`outcome` says, at harness granularity, whether generation ended on the
model's own terms or was interrupted:

```text
Outcome = complete | max_output | content_filter
```

- `complete`: the model finished. A text-only turn with `complete` is a
  finished thought; empty content with `complete` means the model said
  nothing, and what to do about that is caller policy.
- `max_output`: the output budget stopped generation. The turn is known to
  be incomplete — text may be mid-thought and argument fragments may have
  been dropped (see Model Output Repair).
- `content_filter`: the provider's content policy stopped generation.

`outcome` is the only success-side value consumers are expected to branch
on, alongside `tool_calls`. What to do with an incomplete turn — end it,
re-invoke with a continuation nudge, compress and retry — is Runtime
Kernel policy, not this contract's. The same two interruption causes exist
on the failure side as failure codes; which side they appear on depends
only on whether a turn came out the other end. The provider's own reason,
at full fidelity, rides in `finish_reason`.

`finish_reason` is the provider's reported reason for ending generation,
passed through verbatim as an uninterpreted string (`stop`, `length`,
`content_filter`, or anything else a provider reports). It is diagnostic
passthrough, like `provider_response_id`: consumers must not branch on
specific values; they branch on `outcome`, `tool_calls`, and their own
policies.

`labels` is an optional advisory map of string keys to plain string
values:

```text
labels?: map[string]string
```

Labels carry soft classification that no consumer branches on for
correctness: finer-grained outcome classes, provider-specific facts worth
recording, adapter observations that do not fit a typed field. The key
space is open. Labels are non-normative — no invariant, failure path, or
validity rule may depend on them, and any consumer may ignore them
entirely. Specific label conventions are documented in the service
dossier, not in this contract.

`usage`:

```text
CallUsage {
  input_tokens: TokenCount
  output_tokens: TokenCount
  cache_read_tokens?: TokenCount
  cache_write_tokens?: TokenCount
  reasoning_tokens?: TokenCount
}
```

`input_tokens` and `output_tokens` are required. The cache and reasoning
counts are present when the provider reports them. Each count reuses
`TokenCount` from `session.v0.3`: a value plus a `source` of
`char_estimate`, `tokenizer`, or `provider`. Usage is provider-verified
when the provider reports it; otherwise it is estimated, and the source
says so.

`catalog_id` identifies the exact catalog used for this call. It is
required when a catalog was supplied and absent otherwise. Every Model
Invocation record must identify the exact catalog used for that call;
repeated calls with an identical catalog reference the same content-derived
ID.

`model` is the model identity that actually responded. Today routing is
one-shot, so it equals the requested model; once fallback exists it may not,
and the record must always say what ran rather than what was requested.

`provider_response_id` is the provider-side response identifier, when the
adapter surfaces one, used for diagnostics and cost reconciliation.

`repairs` is optional model-output repair evidence:

```text
RepairNote {
  call_id
  kind
  raw_name?
}

RepairKind = name | arguments | truncated
```

`call_id` references a `ToolCall.id` from this result, or — for a
`truncated` note — the id the dropped call would have carried. `kind` says
what was repaired or dropped. `raw_name` carries the name the model
emitted; it is present when `kind` is `name`, and when `kind` is
`truncated` it carries the (possibly partial) name accumulated before the
stream ended, so downstream consumers can say which tool was being called
even though no call was emitted. A `RepairNote` records what the model
emitted versus what this capability produced, so the event log shows the
difference. Raw argument text is not recorded in v0.1; see Model Output
Repair.

A provider that cannot produce a turn produces the failure payload instead;
see Invocation Failure.

Terminal events:

- `model_invocation.invoked`
- `model_invocation.invocation_failed`

## Model Input

```text
ModelInput {
  system?: string
  turns: Turn[]
}
```

`system` is assembled system-prompt content. Layout decisions about what
goes into the system prompt live inside this string.

`turns` is required and must be non-empty. The context renderer compiles
stored session turns into this shape; the names deliberately match the
transcript-side concept so the dataflow needs no bespoke translation.

```text
Turn {
  role
  content?: string
  reasoning?: Evidence
  tool_calls?: ToolCall[]
  call_id?
}

Role = user | assistant | tool
```

`Turn` is provider-neutral. Provider-native payloads belong to the
model-adapter surface and do not appear in this shape.

`role` is required: `user`, `assistant`, or `tool`.

`content` is turn text.

`reasoning` is valid only on `assistant` turns. It carries reasoning
evidence from an earlier call, verbatim, as produced by that call's
`AssistantTurn`. Intermediate layers preserve it untouched; the receiving
adapter decides whether to encode it for its provider. See Reasoning
Evidence Ownership.

`tool_calls` is valid only on `assistant` turns. It carries the normalized
`ToolCall` values returned by an earlier invocation, reused by name from
`tool_invocation.v0`.

`call_id` is valid only on `tool` turns. It references the `ToolCall.id`
this turn answers.

Validity rules:

- a `user` turn requires `content`
- an `assistant` turn requires at least one of `content`, `tool_calls`, or
  `reasoning`
- a `tool` turn requires `call_id` and `content`

A `tool` turn is typically the kernel's projection of a `ToolResult`:
`call_id` from the result and `content` from its model-facing text. This
contract states that derivation without requiring it; the kernel may
produce tool turns however it needs, as long as they satisfy the validity
rules.

An adapter that receives reasoning evidence it recognizes encodes it per
its provider's requirements. An adapter that does not recognize the
evidence kind filters it out. An adapter whose provider would reject the
supplied evidence fails the call with `invalid_request` rather than
silently corrupting the conversation.

## Streaming Observations

`model_invocation.invoke` MAY deliver progressive observations — content
deltas, reasoning deltas, and tool-call starts — to an observer supplied by
the caller alongside the command.

Observations are transport, not events:

- they carry no ordering guarantee relative to other capabilities' events
- they are never appended to the event log
- they impose no replay obligation
- the terminal event alone is authoritative

An adapter that cannot stream delivers nothing progressively. Observers
must tolerate silence and treat the terminal event as the complete truth.
Observations exist so user-facing surfaces can show progress during a call;
any consumer that needs guaranteed content reads the terminal event.

## Invocation Failure

`model_invocation.invocation_failed` is the terminal event when the call
does not produce a turn.

Payload:

```text
ModelInvocationFailure {
  request_id
  code
  message?
  retryable
}
```

`request_id` echoes `ModelInvocationRequest.id`.

`code`:

```text
invalid_request
context_overflow
rate_limited
provider_error
network_error
auth_failed
content_filter
malformed_response
provider_unavailable
cancelled
```

- `invalid_request`: the request was rejected before any provider call
- `context_overflow`: the assembled input exceeds the model's context
  window
- `rate_limited`: the provider refused the call because of rate limits
- `provider_error`: the provider returned an error during the call
- `network_error`: the call could not reach or complete communication with
  the provider
- `auth_failed`: provider credentials were missing, invalid, or rejected
- `content_filter`: the provider refused the call for content policy
- `malformed_response`: the provider response could not be normalized into
  a result
- `provider_unavailable`: the provider is not currently serving or could
  not be reached
- `cancelled`: the call was cancelled before completing

`message` is an optional human-readable detail.

`retryable` means retryable as-is: repeating the same request against the
same (provider, model) pair could reasonably succeed. `rate_limited`,
`provider_error`, `network_error`, and `malformed_response` are retryable;
every other code is not. Remedies that change the request or the route —
compress then retry, fall back to another provider or model, or stop — are
Runtime Kernel decisions, readable from the code.

There is no partial-output field on the failure. Where streaming
observations were delivered, pre-cancellation text already reached the
observer; the failure payload records only that the call did not produce a
turn. Transport failures discard accumulated fragments.

## Model Output Repair

Model output regularly arrives malformed. This service repairs what it can
before returning a normalized result.

Argument repair is string-level repair before parsing:

- loose parsing
- trailing-comma removal
- brace balancing
- control-character escaping

Truncated calls — the stream ended mid-arguments — are never emitted. Each
dropped call is recorded as a `RepairNote` of kind `truncated`, referencing
the id it would have carried and the partial name accumulated before the
stream ended. The provider's reported `finish_reason` stands; a truncation
does not change it. The tool-invocation contract already states that Model
Invocation and the Runtime Kernel own the completeness check.

Complete-but-unrepairable arguments are emitted as an empty object, with a
`RepairNote` of kind `arguments`. Tool Invocation's schema validation is
the backstop that rejects them.

Name repair runs against the supplied catalog:

- The service may normalize and fuzzy-match model-emitted names.
- It emits the canonical name and records the name the model emitted in a
  `RepairNote` of kind `name`.
- It never invents names absent from the catalog.
- Unresolvable names pass through unchanged; `tool_invocation`'s
  `unknown_tool` is the authoritative verdict.

Repair never grants authority. A repaired call still passes Tool
Invocation's exact-match registration, revision, and policy checks like any
other call. Interpretive repair belongs to this model-facing capability;
authoritative resolution belongs to the owning capability.

Repair evidence is capability-local event data. The shared `ToolCall` shape
is not extended with raw model output.

Raw argument text is not recorded in v0.1. Whether a later version records
pre-repair argument text — and how, given size and content sensitivity — is
an open question.

## Unsupported Requests

Unsupported actions return the project-wide `capability.unsupported`
terminal event.

Example:

```text
command: model_invocation.list_models
terminal event: capability.unsupported
```

Model Invocation has one action in v0.1. Provider administration, model
listing, and retry or fallback control are not part of its surface.

## Invariants

- Exactly one terminal event per `model_invocation.invoke`.
- One provider call attempt per command. The service does not retry, back
  off, or fall back internally.
- Every emitted `ToolCall` has a non-empty name and arguments parseable as
  a JSON object; truncated calls are never emitted and are visible as
  `truncated` repair notes.
- Catalog encoding adds, removes, and semantically changes nothing.
- Replay never re-invokes the provider.

## Failure Semantics

Expected failure categories:

- invalid or empty request
- input exceeding the model's context window
- provider rate limits, errors, and unavailability
- network failure reaching the provider
- missing or rejected credentials
- content-filter refusal
- provider response that cannot be normalized
- cancellation

Request validation rejects a malformed request as `invalid_request` before
any provider call:

- a missing `model` or `provider` fails as `invalid_request`
- empty or missing `turns` fails as `invalid_request`
- a turn that violates the role validity rules fails as `invalid_request`

Once a call is in flight, the provider's behavior determines the outcome.
Cancellation and deadlines are carried by the mediator call rather than
copied into the request payload; an honored cancellation produces the
`cancelled` failure code. A provider connection that dies mid-stream is a
retryable failure.

The exact error payload can evolve. The required behavior is that failures
are typed enough for the Runtime Kernel to decide whether to retry,
compress, fall back, or stop.

## Replay

Replay of a recorded invocation returns the recorded terminal event. The
provider is never re-invoked. Streaming observations are not replayed; the
terminal event is the whole truth.

## Reuse

This contract reuses:

- `ToolCall` and `ToolCatalog` from `tool_invocation.v0`
- `TokenCount` from `session.v0.3`

It does not redefine them. Model Invocation consumes `ToolCatalog` and
produces `ToolCall` values; Tool Invocation remains the sole semantic owner
of catalog contents and the authoritative resolver of tool identity.

`session_id` and `turn_id` follow the same correlation convention as the
other contracts: they identify the current session and turn when known, and
receiving them grants no session-record access.

Terminal payloads echo the request `id` as `request_id`, as in the other
contracts.

Unsupported actions return the project-wide `capability.unsupported`
terminal event.

`ModelInput` is defined by this contract and produced by the context
renderer; `docs/context-renderer-capability-contract.md` references it by
name and aligns its vocabulary with the turn-based shape in its next
revision.

`catalog_transition` is processed by the Runtime Kernel between
invocations, not by this capability: after a tool-execution terminal event,
the kernel adopts any transition and supplies the replacement catalog on
the next `model_invocation.invoke`.
