# Context Builder Capability Contract

Date: 2026-08-08

Contract version: `context_builder.v0`.

Status: draft.

This document defines what the rest of the harness may expect from a Context
Builder service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning will be recorded in
`docs/context-builder-service-dossier.md`. Research into existing systems
lives in `docs/research/context-builder-patterns.md`.

## Purpose

Context Builder is the single point of contact for "what the model sees." It
has three actions: estimate resource allocation for a session window, assemble
a byte-stable system prompt from session-scoped inputs, and prepare a
per-turn `ModelInput` from that prompt and the current transcript.

The service:

- owns the division of the model's context window among tools, context
  files, and transcript messages
- assembles the system prompt from configured templates and session-scoped
  inputs
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
  stub: TranscriptStub
}
```

`id` identifies this request. Terminal payloads refer to it as `request_id`.

`model` is required. It is the model identity the session will invoke.

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
system prompt. The caller sizes project instructions, skills text, and tool
snippets to fit within this budget before calling `assemble`.

`max_tools_tokens` is the budget for tool definitions. The caller uses it
to request a sized catalog from Tool Invocation before calling `prepare`.

`max_context_tokens` is the budget for context file content. The caller
uses it to request a sized context bundle from Context Provider before
calling `prepare`.

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

Terminal events:

- `context_builder.estimated`

The estimation is a self-improvement knob. The builder decides the
three-way split among tools, context, and transcript. A different builder
may allocate more tokens to tools and fewer to context; the model can
experiment with allocation policies and measure task success against the
event record.

### `context_builder.assemble`

Assemble the session-scoped system prompt from sized inputs.

Input:

```text
AssembleRequest {
  id
  session_id?
  model
  provider
  instructions: ContextContent[]
  skills: SkillSummary[]
  tool_snippets: ToolSnippet[]
  first_user_message?
}
```

`session_id` identifies the current session when known. Correlation only.

`model` and `provider` are required. They identify the model and provider
the session will invoke. The builder may use them for model-specific prompt
adaptations.

`instructions` is project instruction content, sized per
`Allocation.system_prompt_tokens`. Each entry carries the content the
caller loaded from the workspace:

```text
ContextContent {
  path
  content
}
```

`path` identifies the source file (e.g. `AGENTS.md`). `content` is the
file text, trimmed to fit the system prompt budget. The caller does the
trimming; the builder orders entries.

`skills` is skill summary text for skills enabled for this session:

```text
SkillSummary {
  name
  description
  location
}
```

The builder formats these into the system prompt per its template.

`tool_snippets` is one-line text descriptions of the tools available to the
model:

```text
ToolSnippet {
  name
  description
}
```

`name` is the canonical tool name from the catalog. `description` is a
concise summary. The builder formats these into the system prompt. This is
not the tool schema — `ToolDefinition.input_schema` travels separately
through the catalog. Tool snippets are model-facing awareness text.

`first_user_message` is the first user message of the session, when known.
Optional. The builder may use it for intent-based prompt customization
(e.g. emphasizing relevant tools or skills) or ignore it.

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
  catalog?: ToolCatalog
  context?: ContextEntry[]
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

`catalog` is the canonical `ToolCatalog` from `tool_invocation.v0`, sized
per `Allocation.max_tools_tokens`. It is absent for tool-less turns. The
builder passes it through unchanged; it does not add, remove, or
semantically change definitions.

`context` is context file content from Context Provider, sized per
`Allocation.max_context_tokens`:

```text
ContextEntry {
  file_path
  content
  slot
}
```

`file_path` identifies the source file. `content` is the file text.
`slot` is the `ContextSlot` from `context_provider.v0.1`, re-emitted so
the builder can partition entries by their origin category. The builder
may format or order entries per its template. The caller trims to fit the
budget.

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

- `estimate` must be called before `assemble`. The caller sizes inputs to
  `assemble` using the `Allocation.system_prompt_tokens` budget.
- `assemble` is called once per session unless the kernel explicitly
  invalidates the prefix (model switch, configuration change). Calling it
  again with identical inputs must return an identical `BuiltPrefix`.
- `prepare` is called every turn. It always receives the current
  `BuiltPrefix` and the current transcript.
- `prepare` must not mutate the transcript, catalog, or context entries.
  It produces `ModelInput` from what it receives.
- Catalog and context entries pass through unchanged; the builder does not
  add, remove, or semantically change them. Trimming to fit the budget is
  the caller's responsibility.
- `system_prompt_id` is a SHA-256 hash of the full `system_prompt` text,
  truncated to 16 hex characters.
- The builder is passive: it does not call other capabilities, schedule
  work, or persist state. Its only side effect is the terminal event.

## Failure Semantics

Expected failures:

- missing `model` on any request fails as `invalid_request`
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

- `ToolCatalog` from `tool_invocation.v0`
- `ContextSlot` from `context_provider.v0.1`
- `SessionRecord` from `session.v0.2`
- `ModelInput`, `ModelMessage`, `ModelMessageRole` from
  `model_invocation.v0`

It does not redefine them. Context Builder consumes `ContextSlot` and
`SessionRecord`, produces `ModelInput`, and passes `ToolCatalog` through
unchanged.

`session_id` and `turn_id` follow the same correlation convention as the
other contracts: they identify the current session and turn when known, and
receiving them grants no capability access.

Terminal payloads echo the request `id` as `request_id`, as in the other
contracts.

Unsupported actions return the project-wide `capability.unsupported`
terminal event.
