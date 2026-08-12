# Context Builder Capability Contract

Date: 2026-08-12

Contract version: `context_builder.v0.1`.

Status: draft.

This document defines what the rest of the harness may expect from a Context
Builder service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning live in
`docs/context-builder-service-dossier.md`. Research into existing systems
lives in `docs/research/context-builder-patterns.md`.

`context_builder.v0.1` replaces the standalone input shapes of v0 —
`ContextContent`, `SkillSummary`, `ToolSnippet`, and `ContextEntry` — with
`ContextBundle` from `context_provider.v0.1`. The builder reads slot
information from bundles to decide candidate placement; the kernel no longer
pre-sorts context into type-specific arrays. `ToolCatalog` is available on
`assemble` for tool awareness text but is removed from `prepare` (tools do
not change mid-run). `provider` remains as metadata for builder adaptations.
Estimation remains strictly builder-owned.

## Purpose

Context Builder is the single point of contact for "what the model sees." It
has three actions: estimate resource allocation for a session window, assemble
a byte-stable system prompt from retained context and tool awareness, and
prepare a per-turn `ModelInput` from that prompt, the current transcript, and
per-call context.

The service:

- owns the division of the model's context window among tools, context
  files, and transcript messages
- assembles the system prompt from retained context candidates and tool
  awareness text
- normalizes the session transcript into model-facing messages
- produces a content-addressed identifier for the assembled system prompt
  so invocation events can reference it without inlining

The builder is passive: it does not call other capabilities, schedule work,
or decide when to act. The caller (the Runtime Kernel in v0) owns
orchestration.

## Boundary

Context Builder owns what the model sees: system prompt assembly, message
normalization, and window allocation.

It does not own:

- the session transcript or its persistence. Those belong to Session.
- context file discovery, loading, or content. Those belong to
  Context Provider.
- tool definition, availability, or catalog construction. Those belong to
  Tool Invocation.
- model invocation, provider encoding, or provider-specific constraints.
  Those belong to Model Invocation.
- compression, summarization, or transcript state transforms. Those belong
  to Compression (a separate capability, not yet contracted).
- turn lifecycle, retry, fallback, or cancellation. Those belong to the
  Runtime Kernel.

Context Builder consumes sized inputs produced by other capabilities. It
must not call those capabilities directly. When a catalog or context bundle
must be trimmed to fit a budget, the caller trims it before handing it to
the builder. The builder encodes and orders what it receives without
adding, removing, or semantically changing definitions or content.

## Required Actions

Each action is carried in the project-wide command envelope. The successful
output shown below is the payload of that action's success event. A
direct-call mediator may return the same terminal event to the caller.

### `context_builder.estimate`

Compute how to divide the model's context window among session-scoped inputs.

Input:

```text
EstimateRequest {
  id
  model
  context_window_tokens
  stub: TranscriptStub
}
```

`id` identifies this request. Terminal payloads refer to it as `request_id`.

`model` is required. It is the model identity the session will invoke.

`context_window_tokens` is the model's total context window size in tokens.
Required, must be greater than zero.

`stub` is a lightweight transcript summary. The builder uses it to reserve
space for the messages that Session will materialize:

```text
TranscriptStub {
  message_count
  estimated_tokens
}
```

`message_count` is required. `estimated_tokens` is a rough upper bound; the
caller may supply a char-count heuristic or a tokenizer estimate. The
builder treats it as advisory and may apply its own estimation policy.

Successful terminal output:

```text
Allocation {
  request_id
  system_prompt_tokens
  max_tools_tokens
  max_context_tokens
  max_transcript_tokens
  output_reservation
}
```

`system_prompt_tokens` is the maximum token budget for the builder's own
system prompt. The caller sizes context bundles to fit within this budget
before calling `assemble`.

`max_tools_tokens` is the budget for tool definitions. The caller uses it
to request a sized catalog from Tool Invocation before calling `assemble`.

`max_context_tokens` is the budget for context file content. The caller
uses it to request a sized context bundle from Context Provider before
calling `assemble`.

`max_transcript_tokens` is the budget for the materialized session
transcript. The caller uses it to select or trim messages from Session
before calling `prepare`.

`output_reservation` is the token reservation for model output. The caller
uses it as `max_output_tokens` on the `model_invocation.invoke` request. A
value of zero means the caller should supply its own output budget.

All token values are in the model's tokenizer units unless the builder's
estimation policy uses a different approximation. The allocation is a
budget recommendation, not a strict guarantee. The caller may adjust any
value before using it.

The allocation is the builder's decision. The kernel must not perform
additional math on returned allocation values or override the builder's
budget split. If the kernel needs a different split, it replaces the
builder service — not the allocation after the fact.

Terminal events:

- `context_builder.estimated`

The estimation is a self-improvement knob. The builder decides the
three-way split among tools, context, and transcript. A different builder
may allocate more tokens to tools and fewer to context; the model can
experiment with allocation policies and measure task success against the
event record.

### `context_builder.assemble`

Assemble the session-scoped system prompt from retained context and tool
awareness.

Input:

```text
AssembleRequest {
  id
  session_id?
  model
  provider?
  context: ContextBundle[]
  catalog?: ToolCatalog
}
```

`session_id` identifies the current session when known. Correlation only.

`model` is required. It identifies the model the session will invoke. The
builder may use it for model-specific prompt adaptations.

`provider` is the provider identity. Optional. The builder may use it for
provider-specific prompt adaptations. Provider-specific wire encoding
remains Model Invocation's responsibility.

`context` carries context bundles from Context Provider, sized per
`Allocation.max_context_tokens`. Each bundle contains both retained and
per-call candidates grouped by slot. The builder reads the slot on each
candidate to decide where it belongs in the system prompt. Slot ordering,
block formatting, and candidate layout are builder template decisions. The
kernel does the trimming before calling `assemble`; the builder does not
trim.

```text
ContextBundle {
  retained: ContextCollection
  per_call: ContextCollection
}

ContextCollection {
  buckets: map<ContextSlot, ContextCandidate[]>
  referenced: ContextCandidate[]
}

ContextCandidate {
  id
  content
  refs?: ContextRef[]
}
```

The builder reads the slot on each candidate to decide where it belongs in
the system prompt. Slot ordering, block formatting, and candidate layout are
builder template decisions. The kernel does the trimming before calling
`assemble`; the builder does not trim.

`catalog` is the canonical `ToolCatalog` from `tool_invocation.v0`, sized
per `Allocation.max_tools_tokens`. It is optional — a builder that does not
inject tool awareness text into the system prompt may ignore it. When
present, the builder may extract tool names and descriptions for a tool
awareness block. The full `ToolDefinition` schema travels separately with
Model Invocation, not in the system prompt.

Successful terminal output:

```text
BuiltPrefix {
  request_id
  system_prompt
  system_prompt_id
}
```

`system_prompt` is the assembled system prompt text. It is byte-stable for
the lifetime of the session: calling `assemble` with identical inputs must
produce an identical prompt. The caller delivers it verbatim as
`ModelInput.system` on every `model_invocation.invoke` for the session.

`system_prompt_id` is a content-derived identifier for this prompt. The
caller may include it in invocation events for observability. It is a hash
of the full `system_prompt` text, using SHA-256 truncated to 16 hex
characters. Repeated `assemble` calls with identical inputs produce the same
ID.

Terminal events:

- `context_builder.assembled`

### `context_builder.prepare`

Prepare a per-turn `ModelInput` from the assembled prefix and the current
transcript.

Input:

```text
PrepareRequest {
  id
  session_id?
  turn_id?
  prefix: BuiltPrefix
  transcript: SessionRecord[]
  context: ContextBundle[]
}
```

`session_id` and `turn_id` identify the current session and turn when
known. Correlation only.

`prefix` is required. It is the `BuiltPrefix` returned by the most recent
`assemble` call for this session.

`transcript` is required and must be non-empty. It is the materialized
session transcript from Session, trimmed to fit
`Allocation.max_transcript_tokens`. The builder normalizes it into
model-facing messages: drops scaffolding and errored turns, synthesizes
missing tool results, and converts internal markers to model-readable text.

`context` carries context bundles from Context Provider, sized per
`Allocation.max_context_tokens`. The builder reads the slot on each
candidate to decide injection placement. For example, memory candidates may
be appended to the last user message.

The catalog is not an input to `prepare`. Tools do not change mid-run, and
tool awareness text lives in the system prompt assembled by `assemble`. The
kernel passes the catalog directly to Model Invocation alongside the
`ModelInput` returned by `prepare`.

Successful terminal output:

```text
BuiltContext {
  input: ModelInput
  normalization: NormalizationNote[]
}
```

`input` is the assembled `ModelInput` ready for Model Invocation. `system`
is the `BuiltPrefix.system_prompt`, echoed verbatim. `messages` is the
normalized transcript.

`normalization` records every structural change the builder made to the
transcript. Each note describes one transform:

```text
NormalizationNote {
  transcript_index
  action
  reason
  synthesized_text?
}
```

`transcript_index` is the zero-based position in the original transcript
where this transform applies.

`action` is what the builder did: `dropped` (message removed), `synthesized`
(builder created a new message), `truncated` (message kept but content is
structurally incomplete), or `merged` (two or more transcript messages
combined into one output message).

`reason` is why: `missing_tool_result`, `incomplete_reasoning`,
`mid_stream_abort`, `orphaned_tool_result`, `role_alternation`, or
`empty_turn`.

`synthesized_text` is present only when `action` is `synthesized`. It
carries the text the builder wrote for the message not present in the
original transcript.

The notes are event-level metadata. `ModelInput.messages` carry clean
content. Adapters read the notes to decide per-provider handling; the model
reads them for observability. Every transform is recorded.

Terminal events:

- `context_builder.prepared`

## The System Prompt

The system prompt is assembled from a configured template. The template is
an implementation detail, not part of this contract. The contract guarantees
only:

- the prompt is byte-stable: identical inputs produce identical output
- the prompt carries a content-derived ID for observability
- the prompt is the sole producer of `ModelInput.system`

Template structure, block ordering, formatting, and variable interpolation
are builder implementation choices. Swapping the builder changes the prompt
template; the harness treats the prompt as an opaque string with a stable
ID.

## Invariants

- `estimate` must be called before `assemble` when the kernel intends to
  size inputs. The caller sizes inputs to `assemble` using the returned
  `Allocation` budget fields.
- The allocation is the builder's decision. The kernel must not perform
  additional math on the returned budget values or override the split.
- `assemble` is called once per session unless the kernel explicitly
  invalidates the prefix (model switch, configuration change). Calling it
  again with identical inputs must return an identical `BuiltPrefix`.
- `prepare` is called every turn. It always receives the current
  `BuiltPrefix` and the current transcript.
- `prepare` must not mutate the transcript, catalog, or context entries.
  It produces `ModelInput` from what it receives.
- Context candidates pass through unchanged; the builder does not add,
  remove, or semantically change them. Trimming to fit the budget is
  the caller's responsibility.
- `system_prompt_id` is a SHA-256 hash of the full `system_prompt` text,
  truncated to 16 hex characters.
- The builder is passive: it does not call other capabilities, schedule
  work, or persist state. Its only side effect is the terminal event.

## Failure Semantics

Expected failures:

- missing `model` on any request fails as `invalid_request`
- missing or zero `context_window_tokens` on `estimate` fails as
  `invalid_request`
- empty or missing `transcript` on `prepare` fails as `invalid_request`
- missing `prefix` on `prepare` fails as `invalid_request`
- an unsupported action returns the project-wide `capability.unsupported`
  terminal event

The builder does not own recovery from context overflow, compression
triggering, or budget exhaustion. Those are kernel decisions.

## Replay

Replay of a recorded action returns the recorded terminal event. The
builder is never re-invoked on replay.

## Reuse

This contract reuses:

- `ContextBundle`, `ContextCollection`, `ContextBuckets`, `ContextSlot`,
  `ContextCandidate`, `ContextRef` from `context_provider.v0.1`
- `ToolCatalog`, `ToolDefinition` from `tool_invocation.v0`
- `SessionRecord` from `session.v0.3`
- `ModelInput`, `ModelMessage`, `ModelMessageRole` from
  `model_invocation.v0`

It does not redefine them. Context Builder consumes `ContextBundle` and
`SessionRecord`, produces `ModelInput`, and reads `ToolCatalog` for tool
awareness text in the system prompt.

`session_id` and `turn_id` follow the same correlation convention as the
other contracts: they identify the current session and turn when known, and
receiving them grants no capability access.

Terminal payloads echo the request `id` as `request_id`, as in the other
contracts.

Unsupported actions return the project-wide `capability.unsupported`
terminal event.
