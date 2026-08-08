# Context Builder Service Dossier

Date: 2026-08-08

Contract version: `context_builder.v0`.

Status: draft.

Contract draft: `docs/context-builder-capability-contract.md`.

This dossier records the evidence and boundary decisions behind the Context
Builder capability. It is descriptive working material. The contract is the
stable surface.

## Working Conclusion

Context Builder is the single point of contact for "what the model sees." It
has three actions — `estimate`, `assemble`, `prepare` — that together produce a
per-turn `ModelInput` from session-scoped inputs and the current transcript.

Context Builder owns three things:
- the division of the model's context window among tools, context files, and
  transcript messages (the allocation)
- the byte-stable system prompt assembled from configured templates and
  session-scoped inputs
- the normalized transcript, converted from internal session records to
  model-facing messages

It does not own:
- the session transcript or its persistence (Session)
- context file discovery, loading, or content (Context Provider)
- tool definition, availability, or catalog construction (Tool Invocation)
- model invocation, provider encoding, or provider-specific constraints (Model
  Invocation)
- compression, summarization, or transcript state transforms (Compression, a
  separate future capability)
- turn lifecycle, retry, fallback, or cancellation (Runtime Kernel)

The builder is passive: it does not call other capabilities, does not schedule
work, does not persist state, and does not decide when to act. The Runtime
Kernel orchestrates. The builder receives sized inputs from the kernel and
produces `ModelInput` from what it receives.

## Capability

Context Builder.

## User-Visible Job

Control what the model sees and how the context window is divided.

From a user's point of view, this includes:

- the system prompt is consistent turn-to-turn (byte-stable)
- the model has awareness of its available tools through readable text in the
  system prompt
- the conversation is presented to the model cleanly — broken turns are dropped
  or repaired, not fed to the model raw
- the division of the context window among tools, context files, and
  conversation is a deliberate choice, not an accident
- the assembled prompt carries a content-derived ID so you can verify what the
  model actually received

Swapping this capability changes the system prompt philosophy, how the context
window is budgeted, how transcripts are normalized, and how tools and context
files are introduced to the model.

## Runtime Job

The capability:

- `estimate`: computes an `Allocation` that divides the model's context window
  among system prompt, tools, context files, and transcript, with an output
  reservation for the model's response
- `assemble`: assembles the system prompt from configured templates and
  session-scoped inputs (instructions, skills, tool snippets, optional first
  user message), producing a byte-stable `BuiltPrefix` with a content-derived
  `system_prompt_id`
- `prepare`: produces a per-turn `ModelInput` from the `BuiltPrefix` and the
  current transcript, with optional `ToolCatalog` and `ContextEntry` passthrough

The kernel calls `estimate` once per session (or after invalidation), uses the
`Allocation` to size inputs, calls `assemble` to get the cached prefix, then
calls `prepare` on every turn with that prefix and the materialized transcript.

## Pattern Research

Evidence synthesized from Pi (`/home/mviswanathsai/pi`) and Hermes
(`/home/mviswanathsai/hermes-agent`). Full research in
`docs/research/context-builder-patterns.md`.

### Both systems decompose context assembly into a pipeline of separable concerns

Neither Pi nor Hermes assembles context in one monolithic function. Both
compose independent pieces: system prompt assembly, message selection from the
session, message normalization (custom roles → standard roles, internal marker
stripping), compression/injection of summaries, and dynamic context injection.
Each concern is independently testable in both systems. The pipeline exists;
the seams are known; neither system has contracted them. A Context Builder
contract fills that gap by formalising the assembly step with three
well-defined actions.

### Both systems converge on a minimal output shape: system prompt + messages

Pi produces `Context { systemPrompt?, messages: Message[] }` and passes it to
the model adapter. Hermes produces an `api_messages` list (OpenAI-format dicts)
with the system message first. In both cases, tool schemas travel separately as
a top-level `tools` argument on the model request, not inside messages or the
system prompt. Frankenstein's `ModelInput { system?, messages: ModelMessage[] }`
matches this shape exactly.

### System prompt byte-stability is the supreme invariant

Both systems are built around one invariant: the system prompt must be
byte-stable across turns. Pi builds the system prompt once and caches it;
dynamic content goes into tool results or user messages, never the system
prompt. Hermes builds the system prompt once per session, stores it in the DB,
and restores it verbatim on resume. Memory is a frozen snapshot. Plugin context
is deliberately injected into the user message, not the system prompt. Hermes
code comments are explicit: "system prompt modifications break the prompt cache
prefix." The contract guarantees byte-stability: identical `assemble` inputs
produce identical output.

### Tool schemas travel separately from the builder's output

In both systems, tool schemas are a top-level `tools` field on the model
request. The builder assembles `ModelInput`; the `ToolCatalog` is a separate
argument to `model_invocation.invoke`. The one intersection: both systems
inject tool text snippets (one-line descriptions) into the system prompt for
model awareness. The builder owns that text formatting; the full schema is Tool
Invocation's concern.

### Compression is a distinct concern, not builder responsibility

Both systems use LLM-based summarization, not truncation. Pi's
`prepareCompaction` finds a cut point and calls the model for a summary;
Hermes's `compress` prunes old tool results, protects head and tail, and calls
the model for a summary of the middle. Both reinject summaries as synthetic
user messages. Compression is a state transform on the transcript — it produces
transformed records that the builder consumes. It is a separate capability, not
part of the builder's contract.

## Reconciliation With Existing Contracts

### Session (`session.v0.2`)

Context Builder **consumes** `SessionRecord[]` as the `transcript` input to
`prepare`. It normalizes these internal records into `ModelMessage[]`. It does
not read, write, or mutate session state. The kernel materializes the
transcript from Session and hands it to the builder.

Session's `id` may appear as `session_id` on `assemble` and `prepare` requests
for correlation only; receiving it grants no capability access.

### Context Provider (`context_provider.v0.1`)

Context Builder **consumes** context file content via the `ContextEntry[]` input
to `prepare`. Each `ContextEntry` carries a `slot` field re-emitted from the
provider's `ContextSlot` so the builder can partition entries by origin
category. The builder may format or order entries per its template.

The builder does not call Context Provider. The kernel calls Context Provider,
trims the results to fit `Allocation.max_context_tokens`, and passes them to
`prepare`.

### Tool Invocation (`tool_invocation.v0`)

Context Builder **consumes** the canonical `ToolCatalog` as a passthrough on
the `prepare` request. It does not add, remove, or semantically change tool
definitions. The catalog passes through unchanged to `ModelInput`.

Separately, the builder receives `ToolSnippet[]` (one-line description text) on
`assemble` and formats them into the system prompt. These snippets are not the
tool schema — they are model-facing awareness text. The kernel extracts snippet
text from the catalog; the builder formats it.

Catalog ordering is Tool Invocation's concern. The builder preserves the order
it receives. The builder controls only where the tool block appears in the
system prompt, not the order within the tool list.

### Model Invocation (`model_invocation.v0`)

Context Builder **produces** `ModelInput { system?, messages }` — the shape
defined in the model invocation contract. The kernel delivers this directly as
the input to `model_invocation.invoke`. The builder does not call Model
Invocation.

`ModelInput.system` is the `BuiltPrefix.system_prompt` echoed verbatim.
`ModelInput.messages` is the normalized transcript. `ToolCatalog` travels
separately on the invoke request.

### Session usage (`TokenCount`)

`TokenCount` from `session.v0.2` is reused for allocation token values. All
budget fields in `Allocation` are token counts in the model's tokenizer units
unless the builder's estimation policy uses a different approximation.

## State Owned Or Mutated

A Context Builder service may own:

- the assembled `BuiltPrefix { system_prompt, system_prompt_id }` for the
  current session (cached, not contracted storage)
- the template configuration used during assembly
- estimation policy and any internal tokenizer state

It does not own:

- session state, transcript, or persistence (Session)
- tool definitions, catalog state, or catalog ordering (Tool Invocation)
- context file content, freshness, or source references (Context Provider)
- compression state, compaction windows, or summarization (Compression)
- turn lifecycle, budgets, or cancellation (Runtime Kernel)

The builder is passive. The contract guarantees it does not mutate the
transcript, catalog, or context entries it receives. Its only side effect is
the terminal event appended by the mediator.

## Inputs

### `context_builder.estimate`

- `id` — request identity for terminal event correlation
- `model` — the model identity the session will invoke (required)
- `stub: TranscriptStub` — lightweight transcript summary with `message_count`
  and `estimated_tokens`

### `context_builder.assemble`

- `id` — request identity
- `session_id` (optional) — session correlation only
- `model` — the model identity (required)
- `provider` — the provider identity (required)
- `instructions: ContextContent[]` — project instruction content, sized per
  the allocation budget
- `skills: SkillSummary[]` — enabled skill summaries
- `tool_snippets: ToolSnippet[]` — one-line tool descriptions
- `first_user_message` (optional) — first user message for intent-based prompt
  customization

### `context_builder.prepare`

- `id` — request identity
- `session_id` (optional) — session correlation only
- `turn_id` (optional) — turn correlation only
- `prefix: BuiltPrefix` — the assembled prefix from the most recent `assemble`
  call (required)
- `transcript: SessionRecord[]` — materialized session transcript (required,
  must be non-empty)
- `catalog: ToolCatalog` (optional) — the canonical tool catalog, sized per
  allocation budget
- `context: ContextEntry[]` (optional) — context file content, sized per
  allocation budget

## Outputs

### `context_builder.estimated` (terminal event for `estimate`)

- `Allocation { request_id, system_prompt_tokens, max_tools_tokens,
  max_context_tokens, max_transcript_tokens, output_reservation }`

### `context_builder.assembled` (terminal event for `assemble`)

- `BuiltPrefix { request_id, system_prompt, system_prompt_id }`
  where `system_prompt_id` is a SHA-256 content hash of the full prompt,
  truncated to 16 hex characters

### `context_builder.prepared` (terminal event for `prepare`)

- `ModelInput { system?, messages: ModelMessage[] }`
  where `system` is the echoed `BuiltPrefix.system_prompt` and `messages` is
  the normalized transcript

### Reuse

- `ModelInput`, `ModelMessage`, `ModelMessageRole` are reused from
  `model_invocation.v0`
- `ToolCatalog` is reused from `tool_invocation.v0`
- `ContextSlot` is reused from `context_provider.v0.1`
- `SessionRecord` is reused from `session.v0.2`
- `TokenCount` is reused from `session.v0.2`

## External Effects

None. The builder is passive. It does not call other capabilities, does not
write to any store, does not schedule work, and does not decide when to act.
Its only side effect is the terminal event appended by the mediator.

## Failure Modes

Expected failures:

- `invalid_request` — missing `model` on any request; empty or missing
  `transcript` on `prepare`; missing `prefix` on `prepare`
- `capability.unsupported` — an action the service does not implement

The builder does not own recovery from context overflow, compression
triggering, or budget exhaustion. Those are kernel decisions. If the builder
cannot allocate meaningfully (e.g. the session stub indicates more messages
than the model can hold), it returns an `Allocation` with zero budgets and lets
the kernel decide how to proceed.

## Hidden Coupling To Avoid

- **The builder must not call other capabilities.** It receives sized inputs
  from the kernel and produces `ModelInput`. If the builder called Session for
  more records, or Context Provider for more context, or Tool Invocation for
  catalog updates, it would violate its passive guarantee and create implicit
  orchestration.

- **The builder must not mutate the transcript.** It receives `SessionRecord[]`
  and produces `ModelMessage[]`. It never writes back to Session. Transcript
  mutation (compression, summarization, pruning) is a separate capability.

- **The builder must not decide when to rebuild the prefix.** The kernel
  decides when `assemble` is invalidated (model switch, configuration change).
  The builder treats `assemble` as idempotent: identical inputs always produce
  identical output.

- **Tool snippet text vs. tool schemas.** The builder receives `ToolSnippet[]`
  for system prompt formatting. It must not derive or infer schemas from
  snippets. The full `ToolDefinition` is Tool Invocation's domain.

- **Provider knowledge must not leak into the builder.** The builder receives
  `model` and `provider` for informational template adaptation (e.g. different
  model families use different instruction formats), but it must not encode
  provider-specific wire formats, cache breakpoints, or message ordering
  constraints. Those belong to the Model Invocation adapter.

- **Compression is not the builder's trigger.** The builder consumes whatever
  transcript it receives, which may have been compressed. It does not decide
  when compression should happen or call a compression service.

The proposed contract was stress-tested against both Pi and Hermes: `assemble`
maps 1:1 onto both systems' system prompt builders, `prepare` maps onto both
systems' normalization layers (Hermes's stub synthesis is a verbatim match),
and the pipeline shape exists in both — just not as discrete contracted
operations. `estimate` as a discrete per-session allocation is novel: neither
system has it; both measure reactively against static thresholds. The passive
builder model also conflicts with both systems, whose builders actively call
compressors and mutate session state. This is deliberate: the contract
isolates concerns that both systems intermix.

## Possible Alternate Philosophies

- **Verbatim/full-transcript builder vs normalizing builder.** One builder
  might pass all session records to the model unmodified, preserving every
  internal marker and scaffolding. Another (the reference direction)
  normalizes: drops structurally incomplete turns, synthesizes missing tool
  results, and converts internal markers to model-readable text. The choice
  changes what the model sees and how it interprets conversation history.

- **Template-based vs code-assembled system prompts.** One builder might
  construct the system prompt from a Go text template with variable
  interpolation. Another might assemble it programmatically with conditionals
  per model family. The contract treats the prompt as an opaque string with a
  stable ID; the assembly mechanism is a builder implementation choice.

- **Per-turn estimation vs static allocation.** One builder might re-estimate
  token allocation every turn as tools and context files change. The reference
  direction estimates once per session (or after invalidation) and reuses the
  same budget. A per-turn approach adapts more precisely but adds overhead and
  risks prompt cache invalidation if the system prompt budget changes.

- **Tool-snippets-in-prompt vs tool-snippets-in-messages.** One builder might
  inject tool awareness text into the first user message rather than the
  system prompt, keeping the system prompt smaller for cache efficiency. The
  reference direction puts tool snippets in the system prompt, matching both
  Pi and Hermes.

- **Opaque handle vs explicit BuiltPrefix.** One builder might return an
  opaque handle to a stored prefix rather than the prompt text itself. The
  reference direction returns the text and its content hash — the kernel needs
  the text to pass to Model Invocation anyway, and the hash enables
  observability without storage coupling.

These choices are meaningful enough that Context Builder deserves a capability
contract.

## Language Decision

Go for the v0 Context Builder implementation.

- String assembly, template interpolation, and message normalization are
  string-level operations — they port cleanly from Hermes's Python without
  dynamic-language dependencies.
- The `system_prompt_id` computation is a standard SHA-256 hash, trivial in
  Go's `crypto/sha256`.
- `session`, `model_invocation`, and the Runtime Kernel are all targeted for
  Go. Keeping the builder in Go avoids a cross-language call boundary in the
  hottest path of every turn.
- Token estimation in v0 can use a char-count heuristic or a Go tokenizer
  library; no Python dependency is needed.

This is consistent with the language decision in `model_invocation.v0` and
deliberately revises the AGENTS.md stance of "Python for provider adapters" for
this capability — the same reasoning applies: the work is string-level, and Go
is the runtime language.

## Adjacent: Compression

Compression is a separate future capability, not yet contracted. It owns the
transcript state transform: summarising old messages, pruning tool results,
protecting head and tail, and reintegrating summaries as synthetic user
messages.

The builder consumes whatever transcript it receives. If that transcript has
been compressed, the builder normalizes the compressed records the same way it
normalizes any other records. The builder does not trigger compression, does
not call a compression service, and does not track compression state.

The division is clean: Compression produces a transformed transcript; the
builder consumes it. The kernel owns the decision to compress and the
orchestration between the two.

## Discussion Record

This section preserves design and implementation discussion across working
sessions. It is not normative. Accepted entries are directions for later
contract reconciliation, not changes to the current draft by themselves.

### Accepted Directions

- **Estimate first, then assemble, then prepare.** Estimate must precede
  `assemble` because `assemble` receives inputs sized to the allocation budget.
  `assemble` is called once per session (or after invalidation), not every
  turn. `prepare` is called every turn.

- **Two separate surfaces: assemble (session-scoped, cached) and prepare
  (turn-scoped, called every turn).** Follows the context provider precedent
  (`initialize` + `get_context`). The kernel calls `assemble` once, caches the
  `BuiltPrefix`, and passes it to every `prepare` call.

- **`assemble` returns `BuiltPrefix { system_prompt, system_prompt_id }`** — an
  explicit object, not an opaque handle. The kernel needs the text to pass to
  Model Invocation; the ID enables observability without storage coupling.

- **`system_prompt_id` is a SHA-256 content hash truncated to 16 hex
  characters.** Identical `assemble` inputs produce the same ID. The ID lets
  invocation events reference the prompt without inlining it.

- **`first_user_message` is an optional input to `assemble`** for intent-based
  prompt customization. The builder may use it to emphasise relevant tools or
  skills, or ignore it.

- **Template-as-config: the builder assembles the system prompt from a
  configured template.** Template structure, block ordering, formatting, and
  variable interpolation are implementation details, not contracted. Swapping
  the builder changes the template and prompt philosophy.

- **Allocation is a self-improvement knob.** The builder decides how to divide
  the context window among tools, context files, and transcript. A different
  builder may allocate more tokens to tools and fewer to context. The model can
  experiment with allocation policies and measure task success against the
  event record.

- **`prepare` produces `ModelInput { system, messages }` directly.** The kernel
  doesn't need to know the internal split between system prompt and messages.
  The builder delivers a single structure ready for Model Invocation.

- **The builder normalizes the transcript.** Drops structurally incomplete turns
  (tools with no results, reasoning with no content). Preserves semantically
  valid errors (tool execution failures). Synthesizes missing tool results. The
  normalization policy is the builder's domain; swapping the builder changes
  what the model sees in conversation history.

- **Catalog ordering is Tool Invocation's concern.** The builder preserves the
  order it receives. The builder controls only where the tool snippet block
  appears in the system prompt, not the order within the tool list.

- **Each service owns its domain decisions.** Services that already own a
  domain — Tool Invocation for tool ordering and selection, Context Provider
  for relevance ranking — make those calls. The builder does not override them
  because it lacks the domain knowledge to do so without coupling deeply into
  what those services know. The builder only owns what no other service owns:
  message normalization, system prompt structure, and window allocation. This
  is not a "choose dumb or smart" tradeoff — it is about who has the context
  to make each decision.

- **The builder is passive.** It does not call other capabilities, does not
  schedule work, does not persist state. The kernel orchestrates. This mirrors
  the Model Invocation passivity guarantee.

- **Compression is a separate capability.** The builder receives a transcript
  that may have been compressed; it does not trigger compression, call a
  compression service, or track compression state.

- **Tool schemas travel separately from the builder's output.** Tool snippets
  (one-line descriptions) are part of the system prompt assembly. The full
  `ToolCatalog` travels as a separate argument on `prepare` and passes through
  unchanged to the `ModelInput`.

- **System prompt ordering follows Pi/Hermes convention in v0.** Research
  (Lost in the Middle, primacy/recency studies) confirms that placement within
  the prefix can affect attention, but the system prompt is a small fraction
  of the total context compared to the transcript — placement within it is
  unlikely to be the dominant performance variable. Both Pi and Hermes use
  a fixed static order without documented rationale; following their
  convention is a reasonable default. The bucket-and-slot structure makes
  reordering trivial if empirical evidence later shows it matters for
  a specific model or task.

- **Normalization notes are event-level metadata, not inline text.** A
  separate `normalization` output on `prepare` carries structured notes
  describing what the builder did to the transcript — messages dropped,
  synthesized, truncated, or merged — with tightly bound reasons
  (`missing_tool_result`, `incomplete_reasoning`, `mid_stream_abort`,
  `orphaned_tool_result`, `role_alternation`, `empty_turn`). Messages stay
  clean. The adapter reads notes to decide per-provider handling; the model
  reads notes for observability. This doubles as the normalization
  observability surface — every transform is recorded.

### Implementation Notes

- Go for v0 implementation, consistent with the `model_invocation` language
  decision. String-level operations, standard-library crypto, and same-process
  call path avoid unnecessary cross-language boundaries.

- The `estimate` stub is minimal in v0: `TranscriptStub { message_count,
  estimated_tokens }`. The builder may use a char-count heuristic or a
  tokenizer. Future versions may add more precise estimation without changing
  the envelope shape.

- Template configuration is an implementation detail. The v0 reference may use
  Go `text/template` with a TOML or JSON template file. The contract does not
  specify the template format or block structure.

### Open Questions

- **Message normalization boundary.** Where does harness-side normalization end
  and provider-side normalization begin? The builder normalizes harness-internal
  records to standard model messages. The Model Invocation adapter sanitizes per
  provider (e.g. Anthropic's alternating-role constraint). The exact split of
  responsibilities may need refinement as more providers are integrated.

- **Token estimation method.** Does the builder use a char-count heuristic, a
  Go tokenizer library (e.g. `tiktoken-go`), or rely on provider-reported usage
  from prior turns? The contract treats `TranscriptStub.estimated_tokens` as
  advisory. The builder's own estimation policy is implementation-defined.

- **Artifact store integration for `system_prompt_id` resolution.** The
  contract defines `system_prompt_id` as a content hash, but does not specify
  where prompts are stored for later retrieval. An artifact store integration
  is deferred to later contract versions.

- **Model-family and provider constraints on estimation.** Different models
  have different tool encoding overheads (e.g. Anthropic's tool use format
  consumes more tokens than OpenAI's function-calling format). The builder may
  need model-family awareness for accurate allocation, but the contract leaves
  this as an implementation detail.

- **Per-turn re-estimation for mid-session changes.** When tools or context
  files change mid-session (e.g. a new tool is registered), should the kernel
  re-estimate the allocation? Not needed in v0, but the contract leaves room
  for the kernel to call `estimate` at any time.

- **`output_reservation` semantics.** The contract says a zero value means the
  caller should supply its own output budget. Whether the builder should always
  provide a non-zero reservation or default to leaving it to the kernel is an
  open implementation choice.

- **Skill vs instruction ordering.** The builder receives `instructions` and
  `skills` as separate inputs. How they are ordered and formatted relative to
  each other in the system prompt is a template decision. The contract does not
  prescribe ordering.
