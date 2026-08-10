# Model Invocation Capability Contract

Date: 2026-08-07

Contract version: `model_invocation.v0`.

Status: draft.

This document defines what the rest of the harness may expect from a Model
Invocation service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning live in
`docs/model-invocation-service-dossier.md`. This contract closes the
tool-invocation interaction: canonical catalog to provider encoding,
normalized tool calls, and provider replay metadata.

## Purpose

Model Invocation is the harness's single point of contact with model
providers.

It has one action: perform one model call against assembled
provider-neutral input plus a canonical tool catalog, and return the
normalized output.

The service:

- encodes the input and catalog for the selected provider
- performs the call
- accumulates streamed fragments into a complete response
- repairs malformed model output
- produces a normalized result

Streaming is an internal transport detail of the call. This contract
defines no intermediate stream events; the terminal event is the semantic
record of the call.

The base flow, seen from this side, is:

```text
kernel -> tool_invocation.list_tools
kernel -> assembles ModelInput from the context bundle and materialized
          session
kernel -> model_invocation.invoke with input, catalog, and model
model invocation -> normalized content, tool_calls, usage, stop_reason
kernel -> tool_invocation.execute with the returned tool_calls
kernel -> processes the tool-execution terminal event and any
          catalog_transition
kernel -> model_invocation.invoke again when continuing
```

The first implementation may use direct in-process calls through the
mediator. Nothing in this contract requires a separate process or an event
bus.

## Boundary

Model Invocation owns the provider-facing encoding of one request, the
provider call itself, and the normalization and repair of the response.

It does not own:

- deciding what the model sees. That is the builder role, which is not yet
  contracted; the Runtime Kernel plays it in v0.
- tool execution or authoritative tool resolution. Those belong to Tool
  Invocation.
- retry, backoff, fallback, or continuation decisions. Those belong to the
  Runtime Kernel, whose contract is not yet drafted.
- session record writes. Those belong to Session.
- turn lifecycle, budgets, or cancellation. Those belong to the Runtime
  Kernel.

Model Invocation consumes two artifacts produced elsewhere: the `ModelInput`
assembled by the builder and an immutable `ToolCatalog` from Tool
Invocation. It must not construct or modify either. It may encode the
catalog for a provider, but it must not add, remove, or semantically change
canonical definitions.

## Required Actions

Each action is carried in the project-wide command envelope. The successful
output shown below is the payload of that action's success event. A direct-call
mediator may return the same terminal event to the caller.

### `model_invocation.invoke`

Perform one model call against assembled provider-neutral input and return
the normalized result.

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
  max_output_tokens?
}
```

`id` identifies this request. Terminal payloads refer to it as `request_id`.
It is an identity and correlation value, not a durable write idempotency
key.

`session_id` identifies the current session when known. It provides
correlation only. Merely receiving it does not grant session-record access.

`turn_id` identifies the current turn when known. It is issued by the
Runtime Kernel. The Runtime Kernel contract is not yet drafted, so this
reference is a correlation convention rather than a typed promise.

`model` is required. It is the model identity to invoke.

`provider` is required. It identifies the provider to route this call to.
The Runtime Kernel supplies it alongside `model`; the service dispatches
to the configured adapter for that provider.

`input` is required. It is the assembled provider-neutral model input. The
service encodes it for the selected provider; it does not take over the
builder's layout decisions.

`catalog` is the `ToolCatalog` defined by `tool_invocation.v0`, reused by
name. It is absent for tool-less calls. When supplied, the service encodes
it for the selected provider without adding, removing, or semantically
changing definitions, preserves the catalog order when the selected
provider permits, and echoes the catalog identity as `catalog_id` on the
success payload. The canonical catalog is distinct from the provider-encoded
bundle; the service retains that exact encoded bundle, or a durable
reference to it, when provider-level replay is required.

`max_output_tokens` is an optional integer output budget. Omission means
the service default.

Successful terminal output:

```text
ModelInvocationResult {
  request_id
  content?: string
  reasoning?: string
  tool_calls: ToolCall[]
  stop_reason
  usage: CallUsage
  catalog_id?
  model
  provider_response_id?
  repairs?: RepairNote[]
}
```

`request_id` echoes `ModelInvocationRequest.id`.

`content` is the normalized assistant text. It is optional; when present it
may be empty.

`reasoning` is echo-back evidence with the same semantics as
`ModelMessage.reasoning`: opaque provider evidence that the same provider
requires echoed back on subsequent calls. The Runtime Kernel obtains it
from this result and must preserve it verbatim in later `ModelInput`. This
service does not interpret it.

`tool_calls` is required and may be empty. Every emitted call is complete,
normalized, and uses canonical names. Model Invocation assigns each emitted
call's `id`, unique within the result. When a catalog was supplied and an
emitted name mapped to a definition in it, the call carries that
definition's `tool_id` and `definition_revision`; when the name did not
map, they are absent together. They are not model-written arguments.

`stop_reason`:

```text
StopReason = end_turn | tool_calls | max_output | content_filter
```

- `end_turn`: the model finished its response normally
- `tool_calls`: the model requested tool execution
- `max_output`: generation stopped at the output budget
- `content_filter`: the provider stopped generation for content policy

A provider that rejects a call outright produces the `content_filter`
failure code instead; `stop_reason` appears only on a successful result.

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
`TokenCount` from `session.v0.2`: a value plus a `source` of
`char_estimate`, `tokenizer`, or `provider`. Usage is provider-verified
when the provider reports it; otherwise it is estimated, and the source
says so.

`catalog_id` identifies the exact catalog used for this call. It is
required when a catalog was supplied and absent otherwise. Every Model
Invocation record must identify the exact catalog used for that call;
repeated calls with an identical catalog reference the same content-derived
ID.

`model` is the model that actually responded, so the record says what ran
rather than what was requested.

`provider_response_id` is the provider-side response identifier, used for
diagnostics and cost reconciliation.

`repairs` is optional model-output repair evidence:

```text
RepairNote {
  call_id
  kind
  raw_name?
}

RepairKind = name | arguments
```

`call_id` references the `ToolCall.id` of the repaired call. `kind` says
what was repaired. `raw_name` carries the name the model emitted and is
present only when `kind` is `name`. A `RepairNote` records what the model
emitted versus what this capability produced, so the event log shows the
difference. Raw argument text is not recorded in v0; see Model Output
Repair.

Terminal events:

- `model_invocation.invoked`
- `model_invocation.invocation_failed`

## Model Input

```text
ModelInput {
  system?: string
  messages: ModelMessage[]
}
```

`system` is assembled system-prompt content. In v0, layout decisions about
what goes into the system prompt live inside this string.

`messages` is required and must be non-empty.

```text
ModelMessage {
  role
  content?: string
  reasoning?: string
  tool_calls?: ToolCall[]
  call_id?
}

ModelMessageRole = user | assistant | tool
```

`ModelMessage` is provider-neutral. In the same sense that
`SessionRecord.text` is normalized text rather than provider-formatted
replay data, provider-native payloads belong to the model-adapter surface
and do not appear in this shape.

`role` is required: `user`, `assistant`, or `tool`.

`content` is message text.

`reasoning` is valid only on `assistant` messages. It is opaque provider
evidence from an earlier call that the same provider requires echoed back
on subsequent calls. Producers of later `ModelInput` must preserve it
verbatim; the Runtime Kernel obtains it from a prior
`ModelInvocationResult`.

`tool_calls` is valid only on `assistant` messages. It carries the
normalized `ToolCall` values returned by an earlier invocation, reused by
name from `tool_invocation.v0`.

`call_id` is valid only on `tool` messages. It references the `ToolCall.id`
this message answers.

Validity rules:

- a `user` message requires `content`
- an `assistant` message requires at least one of `content`, `tool_calls`,
  or `reasoning`
- a `tool` message requires `call_id` and `content`

A `tool` message is typically the kernel's projection of a `ToolResult`:
`call_id` from the result and `content` from its model-facing text. This
contract states that derivation without requiring it; the kernel may
produce tool messages however it needs, as long as they satisfy the
validity rules.

## Invocation Failure

`model_invocation.invocation_failed` is the terminal event when the call
does not produce a result.

Payload:

```text
ModelInvocationFailure {
  request_id
  code
  message?
  retryable
  partial?: PartialOutput
}

PartialOutput {
  content?: string
  reasoning?: string
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

`partial` is populated only for `cancelled` failures, where it carries the
normalized text and reasoning evidence accumulated before cancellation.
Transport failures discard partial output; `partial` is absent for them.

## Model Output Repair

Model output regularly arrives malformed. This service repairs what it can
before returning a normalized result.

Argument repair is string-level repair before parsing:

- loose parsing
- trailing-comma removal
- brace balancing
- control-character escaping

Truncated calls — the stream ended mid-arguments — are never emitted. The
tool-invocation contract already states that Model Invocation and the
Runtime Kernel own that completeness check.

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

Raw argument text is not recorded in v0. Whether a later version records
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

Model Invocation has one action in v0. Provider administration, model
listing, and retry or fallback control are not part of its surface.

## Invariants

- Exactly one terminal event per `model_invocation.invoke`.
- One provider call attempt per command. The service does not retry, back
  off, or fall back internally.
- Every emitted `ToolCall` has a non-empty name and arguments parseable as
  a JSON object, and truncated calls are never emitted.
- If `tool_calls` is non-empty, `stop_reason` is `tool_calls`.
- Catalog encoding adds, removes, and semantically changes nothing.
- A successful result echoes `catalog_id` when a catalog was supplied, and
  always identifies the model that actually responded.
- The service performs no session writes, no tool execution, and no
  continuation decisions.
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

- a missing `model` fails as `invalid_request`
- empty or missing `messages` fails as `invalid_request`
- a message that violates the role validity rules fails as
  `invalid_request`

Once a call is in flight, the provider's behavior determines the outcome.
Cancellation and deadlines are carried by the mediator call rather than
copied into the request payload; an honored cancellation produces the
`cancelled` failure code with accumulated partial text preserved in
`partial`. A provider connection that dies mid-stream is a retryable
failure; partial output is discarded.

The exact error payload can evolve. The required behavior is that failures
are typed enough for the Runtime Kernel to decide whether to retry,
compress, fall back, or stop.

## Replay

Replay of a recorded invocation returns the recorded terminal event. The
provider is never re-invoked.

## Reuse

This contract reuses:

- `ToolCall` and `ToolCatalog` from `tool_invocation.v0`
- `TokenCount` from `session.v0.2`

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

`catalog_transition` is processed by the Runtime Kernel between
invocations, not by this capability: after a tool-execution terminal event,
the kernel adopts any transition and supplies the replacement catalog on
the next `model_invocation.invoke`.
