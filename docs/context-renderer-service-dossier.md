# Context Renderer Service Dossier

Date: 2026-08-23

Contract version: `context_renderer.v0.3`.

Status: draft.

Contract: `docs/context-renderer-capability-contract.md`.

Formerly the Context Builder service dossier. The capability was renamed from
Context Builder to Context Renderer when `context_builder.v0.2` collapsed into
`context_renderer.v0.3`; this dossier was rewritten for the renamed
capability. The old builder-era discussion records are preserved in condensed
form under Superseded Directions.

This dossier records the evidence and boundary decisions behind the Context
Renderer capability. It is descriptive working material. The contract is the
stable surface.

## Working Conclusion

Context Renderer is the single point of contact for "what the model sees." It
has one action — `render` — that produces a per-turn `ModelInput` from the
session transcript, the provider's current context response, and a
session-scoped `config` slot.

Context Renderer owns three things:

- the system prompt, derived deterministically from `config` through a
  template, with a content-derived `system_prompt_id`
- the normalized transcript, converted from internal session records to
  model-facing turns
- dynamic-context delivery: the provider's candidates rendered into the model-
  facing turns per the renderer's template policy — never into the system prompt

It does not own:

- the session transcript or its persistence (Session)
- dynamic context discovery, loading, or content (Context Provider)
- stable context production: identity, instructions, skills, tool guidance
  arrive inside `config`; the renderer consumes them, it does not discover
  them (the runtime sources them from `context_provider.get_stable_context`)
- tool definition, availability, or catalog construction (Tool Invocation)
- model invocation, provider encoding, or provider-specific constraints
  (Model Invocation)
- compression, summarization, or transcript state transforms (Compression, a
  separate future capability)
- turn lifecycle, retry, fallback, or cancellation (Runtime Kernel)
- context-window allocation. `estimate` is gone; the caller sizes the
  transcript and dynamic context before calling `render`.

The renderer is passive: it does not call other capabilities, does not
schedule work, does not persist state, and does not decide when to act. The
Runtime Kernel orchestrates. The renderer never mutates its inputs;
transformations happen only in the `ModelInput` it returns.

## Capability

Context Renderer.

## User-Visible Job

Control what the model sees.

From a user's point of view, this includes:

- the system prompt is byte-stable across turns; it changes only on a
  deliberate reassembly (model switch, configuration change, or an explicit
  kernel decision)
- the model has awareness of its available tools through readable text in the
  system prompt
- the conversation is presented to the model cleanly — scaffolding is dropped
  and broken turns repaired, not fed to the model raw
- dynamic context (memory, discovered files) reaches the model in a
  structured, attributable form
- the rendered prompt carries a content-derived ID so you can verify what the
  model actually received

Swapping this capability changes the system prompt philosophy, the template,
how transcripts are normalized, and how dynamic context is delivered to the
model.

## Runtime Job

The capability:

- `render`: derives the system prompt and its content-derived
  `system_prompt_id` from `config`, normalizes the transcript into
  model-facing turns, renders the dynamic context candidates into the
  model-facing turns per its template policy, and returns a `ModelInput` ready
  for Model Invocation

The kernel builds `config` once per session and holds it; every `render` call
in the session receives the identical config. Reassembly — supplying a
changed config — is a deliberate kernel decision. The kernel calls `render`
once per inner-loop iteration with the current transcript and the current
`ContextResponse`.

## Pattern Research

Evidence synthesized from Pi (`/home/mviswanathsai/pi`) and Hermes
(`/home/mviswanathsai/hermes-agent`). Full research in
`docs/research/context-builder-patterns.md`.

### Both systems decompose context assembly into a pipeline of separable concerns

Neither Pi nor Hermes assembles context in one monolithic function. Both
compose independent pieces: system prompt assembly, message selection from
the session, message normalization, compression/injection of summaries, and
dynamic context injection. The pipeline exists; the seams are known; neither
system has contracted them. The renderer contract formalizes the assembly
step with a single well-defined action.

### Both systems converge on a minimal output shape: system prompt + messages

Pi produces `Context { systemPrompt?, messages: Message[] }` and passes it to
the model adapter. Hermes produces an `api_messages` list with the system
message first. In both cases, tool schemas travel separately as a top-level
`tools` argument on the model request. Frankenstein's
`ModelInput { system?, turns: Turn[] }` matches this shape (as of
`model_invocation.v0.1`; the v0 shape used `messages`).

### System prompt byte-stability is the supreme invariant

Both systems are built around one invariant: the system prompt must be
byte-stable across turns. Pi builds the system prompt once and caches it;
dynamic content goes into tool results or user messages, never the system
prompt. Hermes builds the system prompt once per session, stores it in the
DB, and restores it verbatim on resume. Hermes code comments are explicit:
"system prompt modifications break the prompt cache prefix." The v0.3
contract guarantees byte-stability through determinism: the prompt is a
deterministic function of `config` alone, and the caller supplies a changed
config only on deliberate reassembly.

### Tool schemas travel separately from the renderer's output

In both systems, tool schemas are a top-level `tools` field on the model
request. The renderer produces `ModelInput`; the `ToolCatalog` is a separate
argument to `model_invocation.invoke`. The one intersection: both systems
inject tool text snippets (one-line descriptions) into the system prompt for
model awareness. The renderer owns that text formatting (the catalog arrives
inside `config`); the full schema is Tool Invocation's concern.

### Compression is a distinct concern, not renderer responsibility

Both systems use LLM-based summarization, not truncation. Pi's
`prepareCompaction` finds a cut point and calls the model for a summary;
Hermes's `compress` prunes old tool results, protects head and tail, and calls
the model for a summary of the middle. Both reinject summaries as synthetic
user messages. Compression is a state transform on the transcript — it
produces transformed records that the renderer consumes. It is a separate
capability, not part of the renderer's contract.

## Reconciliation With Existing Contracts

### Session (`session.v0.3`)

Context Renderer **consumes** `SessionRecord[]` as the `transcript` input to
`render`. It normalizes these internal records into `Turn[]`. Reasoning evidence on
assistant records is carried through verbatim as opaque `Evidence`. It does
not read, write, or mutate session state. The kernel materializes the
transcript from Session and hands it to the renderer.

Session's `id` may appear as `session_id` on the render request for
correlation only; receiving it grants no capability access.

### Context Provider (`context_provider.v0.2`)

Context Renderer **consumes** `ContextResponse` as the `dynamic_context`
input to `render` — the shared flat shape returned by both
`get_dynamic_context` (per turn) and `get_stable_context` (once per session).

The renderer renders the dynamic response's candidates into the model
messages. Stable material never arrives as dynamic candidates; the runtime
freezes the stable response into the renderer's `config`, where the renderer
consumes it as `Material` sections. Section names ride in candidate
`metadata` (`slot` convention: `identity`, `instructions`, `skills`,
`tool_guidance`). Templating on `metadata.slot` is an implementation choice —
the runtime and renderer may key on it however they want; nothing in either
contract depends on it.

The renderer does not call Context Provider. The kernel calls both provider
actions and hands the results to the renderer.

### Tool Invocation (`tool_invocation.v0`)

Context Renderer **consumes** the canonical `ToolCatalog` inside `config` for
tool awareness text in the system prompt. The renderer extracts tool names
and descriptions from `ToolDefinition` entries and formats them per its
template.

The kernel fetches the catalog once per session and freezes it into `config`;
the prompt's tool awareness text is session-stable. The same session catalog
is the invocation catalog. Mid-turn `ToolCatalogTransition` values from tool
execution are typed runtime control flow and observability evidence — they
record what the model effectively saw through tool searches and describes —
never model-facing text. The renderer's prompt never changes mid-session.

Catalog ordering is Tool Invocation's concern. The renderer preserves the
order it receives for the tool awareness block.

### Model Invocation (`model_invocation.v0.1`)

Context Renderer **produces** `ModelInput { system?, turns }` — the shape
defined in the model invocation contract. The kernel delivers this directly
as the input to `model_invocation.invoke`. The renderer does not call Model
Invocation.

`ModelInput.system` is the system prompt derived from `config`.
`ModelInput.turns` is the normalized transcript plus the rendered
dynamic-context turns. `ToolCatalog` travels separately on the invoke
request.

## State Owned Or Mutated

A Context Renderer service may own:

- internal caches or derived state keyed by inputs (the contract allows it;
  none of it may make outputs vary for identical inputs)
- the template configuration used during prompt derivation

It does not own:

- session state, transcript, or persistence (Session)
- tool definitions, catalog state, or catalog ordering (Tool Invocation)
- dynamic context content, freshness, or source references (Context Provider)
- the session's `config` — `config` is an input the caller holds and supplies;
  the renderer never mutates it
- compression state, compaction windows, or summarization (Compression)
- turn lifecycle, budgets, or cancellation (Runtime Kernel)

The renderer is passive. The contract guarantees it does not mutate the
transcript, dynamic context, or config it receives. Its only externally
observable effects are its terminal events.

## Inputs

### `context_renderer.render`

- `id` — required, request identity for terminal event correlation
- `session_id?` — correlation only
- `transcript: SessionRecord[]` — required, non-empty; the materialized
  session transcript, trimmed by the caller to its budget
- `dynamic_context: ContextResponse` — required; missing is
  `invalid_request`, an empty candidate list is valid
- `config` — required; the session-scoped material slot (see below)

The Go request types use pointers for `dynamic_context` and `config` so the
implementation can distinguish "missing" from "zero".

### The `config` slot (pairing policy)

The contract requires the slot's presence, not its shape. The Go pairing
shape between the kernel and the reference renderer:

```text
Config {
  Material: MaterialSection[]   // stable material, in order
  Tools:    ToolCatalog?        // session-frozen catalog for tool awareness
  Model:    string              // carried; not rendered by the default template
}

MaterialSection {
  Name:    string               // e.g. identity, instructions, skills, tool_guidance
  Content: string
}
```

`Material` is slice-based so template iteration is deterministic with no map
sorting. A zero-valued `Config` (empty material, nil catalog) is valid but
degenerate — see Open Questions.

## Outputs

### `context_renderer.rendered` (terminal event for `render`)

- `RenderResult { request_id, input: ModelInput, system_prompt_id }`
  where `input.system` is the derived system prompt, `input.messages` is the
  normalized transcript plus rendered dynamic-context messages, and
  `system_prompt_id` is a SHA-256 content hash of the full `system` text,
  truncated to 16 hex characters

### Reuse

- `ContextResponse`, `ContextCandidate`, `ContextFailure`, `ContextRef` are
  reused from `context_provider.v0.2`
- `ToolCatalog`, `ToolDefinition` are reused from `tool_invocation.v0`
- `SessionRecord` is reused from `session.v0.3`
- `ModelInput`, `Turn`, `Role`, `Evidence` are reused from
  `model_invocation.v0.1`

## External Effects

None. The renderer is passive. It does not call other capabilities, does not
write to any store, does not schedule work, and does not decide when to act.
Its only externally observable effects are its terminal events.

## Failure Modes

Expected failures:

- `invalid_request` — missing `id`; missing or empty `transcript`; missing
  `dynamic_context`; missing `config`
- `template_error` — the prompt template failed to render; a config or
  deployment problem, distinguished from a code bug in the event log
- `internal_error` — anything unexpected

`capability.unsupported` is the project-wide terminal event for unsupported
actions, handled by the mediator.

The renderer does not own recovery from context overflow, compression
triggering, or budget exhaustion. Those are kernel decisions.

## Hidden Coupling To Avoid

- **The renderer must not call other capabilities.** It receives sized inputs
  from the kernel and produces `ModelInput`. If the renderer called Session
  for more records, or Context Provider for more context, or Tool Invocation
  for catalog updates, it would violate its passive guarantee and create
  implicit orchestration.

- **The renderer must not mutate its inputs.** It receives `SessionRecord[]`,
  a `ContextResponse`, and `config`, and produces `ModelInput`. It never
  writes back to Session or the provider. Transcript mutation (compression,
  summarization, pruning) is a separate capability.

- **The renderer must not decide when reassembly happens.** The kernel
  decides when to supply a changed `config` (model switch, configuration
  change, explicit kernel decision). The renderer treats `config` as an
  input, never as state it mutates.

- **Dynamic content must never enter the system prompt.** The provider's
  candidates render into the model messages. The system prompt is derived
  from `config` alone; `render` is the sole producer of `ModelInput.system`.

- **Tool snippet text vs. tool schemas.** The renderer formats tool names and
  descriptions for the prompt. It must not derive or infer schemas from
  snippets. The full `ToolDefinition` is Tool Invocation's domain.

- **Provider knowledge must not leak into the renderer.** The renderer must
  not encode provider-specific wire formats, cache breakpoints, or message
  ordering constraints. Those belong to the Model Invocation adapter.

- **Compression is not the renderer's trigger.** The renderer consumes
  whatever transcript it receives, which may have been compressed. It does
  not decide when compression should happen or call a compression service.

## Possible Alternate Philosophies

- **Verbatim/full-transcript renderer vs normalizing renderer.** One renderer
  might pass all session records to the model unmodified, preserving every
  internal marker and scaffolding. Another (the reference direction)
  normalizes: drops structurally incomplete turns, synthesizes missing tool
  results, and converts internal markers to model-readable text. The choice
  changes what the model sees and how it interprets conversation history. The
  reference's repair transforms are acknowledged to be v0-grade; a later pass
  redefines them well (see Discussion Record).

- **Template-based vs code-assembled system prompts.** One renderer might
  construct the system prompt from a Go text template with variable
  interpolation. Another might assemble it programmatically with conditionals
  per model family. The contract treats the prompt as an opaque string with a
  stable ID; the assembly mechanism is a renderer implementation choice.

- **Dynamic candidates in the last user message vs dedicated messages.** The
  reference appends rendered candidates to the last user message, preserving
  provider order. A renderer might instead emit dedicated user-role messages,
  which are more salient to the model but fabricate messages and need
  placement rules. The contract leaves this to template policy.

- **Opaque handle vs explicit prompt.** One renderer might return an opaque
  handle to a stored prompt rather than the text itself. The reference
  direction returns the text and its content hash — the kernel needs the text
  to pass to Model Invocation anyway, and the hash enables observability
  without storage coupling.

These choices are meaningful enough that Context Renderer deserves a
capability contract.

## Language Decision

Go for the v0 Context Renderer implementation.

- String assembly, template interpolation, and message normalization are
  string-level operations — they port cleanly from Hermes's Python without
  dynamic-language dependencies.
- The `system_prompt_id` computation is a standard SHA-256 hash, trivial in
  Go's `crypto/sha256`.
- `session`, `model_invocation`, and the Runtime Kernel are all targeted for
  Go. Keeping the renderer in Go avoids a cross-language call boundary in the
  hottest path of every turn.

This is consistent with the language decision in `model_invocation.v0` and
deliberately revises the AGENTS.md stance of "Python for provider adapters"
for this capability — the same reasoning applies: the work is string-level,
and Go is the runtime language.

## Adjacent: Compression

Compression is a separate future capability, not yet contracted. It owns the
transcript state transform: summarising old messages, pruning tool results,
protecting head and tail, and reintegrating summaries as synthetic user
messages.

The renderer consumes whatever transcript it receives. If that transcript has
been compressed, the renderer normalizes the compressed records the same way
it normalizes any other records. The renderer does not trigger compression,
does not call a compression service, and does not track compression state.

The division is clean: Compression produces a transformed transcript; the
renderer consumes it. The kernel owns the decision to compress and the
orchestration between the two.

## Discussion Record

This section preserves design and implementation discussion across working
sessions. It is not normative. Accepted entries are directions for later
contract reconciliation, not changes to the current draft by themselves.

### v0.3 Session (2026-08-23)

The capability was renamed to Context Renderer and collapsed to one action.
All entries below were accepted in the session that produced
`context_renderer.v0.3`; they are labeled Accepted (direction),
Implementation Choice (tentative, reference-implementation level), or Open.

#### Accepted Directions

- **One action: `render`.** `estimate`, `assemble`, and `prepare` collapse
  into a single action. `estimate` was never called by the kernel; window
  sizing moves to the caller. `assemble` and `prepare` merge — the system
  prompt derivation and the message assembly happen in one pass.

- **Normalization behavior stays; the notes output goes.** The renderer keeps
  the transform behavior (drop scaffolding such as system notes, synthesize
  missing tool results, drop orphaned tool results, convert internal markers
  to model-readable text). The `NormalizationNote[]` output is dropped —
  structured recording of transforms is deferred to the event-model pass and
  is not part of the contract's output. The kernel never consumed the notes.

- **The system prompt is a deterministic function of `config` alone.**
  Identical `config` always produces an identical prompt and
  `system_prompt_id` (SHA-256 of the full prompt text, truncated to 16 hex
  characters). The prompt never varies with transcript, dynamic context, turn
  count, or elapsed time. Byte-stability is the corollary, and it is what
  keeps provider prompt caches warm.

- **`config` is built once per session and held by the kernel.** The kernel
  builds config (stable material + session-frozen catalog + model) once per
  session and passes the identical value to every `render` call. A changed
  config is supplied only on deliberate reassembly: model switch,
  configuration change, or an explicit kernel decision. The old
  `built_prefix` session-metadata cache and its load/store machinery are
  removed — determinism makes them unnecessary.

- **The catalog is fetched once per session.** One `list_tools` call per
  session feeds both the prompt's tool awareness text (inside config) and the
  invocation catalog. No per-turn refresh. Per-call tool-list changes destroy
  provider cache prefixes and buy nothing — new tools are reached through the
  proxy tool path, not mid-session registration. This is a kernel behavior,
  not a contract requirement.

- **Catalog transitions are observability evidence, never model-facing
  text.** Mid-turn `ToolCatalogTransition` values are processed per the tool
  invocation contract as typed runtime control flow — the kernel compares the
  transition base and adopts or refreshes — and they record what the model
  effectively saw through tool searches and describes. They never change the
  system prompt; the renderer's config stays frozen mid-session.

- **Stable material is sourced from the provider, not read by the kernel.**
  `context_provider.get_stable_context` returns the stable material once per
  session; the kernel freezes it into `config.Material`. The kernel does not
  duplicate the provider's discovery and classification machinery (AGENTS.md,
  CLAUDE.md, CURSOR.md, SOUL.md, and similar convention files are provider
  discovery, not kernel loading). Stable files never arrive as dynamic
  candidates, so nothing renders twice.

- **`metadata.slot` is a convention, not a contract.** Section names ride in
  candidate `metadata` on the stable response (`identity`, `instructions`,
  `skills`, `tool_guidance`). Templating on `metadata.slot` is an
  implementation choice — anyone can do it however they want. No guarantee in
  either contract depends on any metadata key.

- **Provider response shape is shared.** `get_dynamic_context` and
  `get_stable_context` return the same flat `ContextResponse` shape
  (`{request_id, candidates, failures}`). The v0.1-era `DynamicContext` name
  is retired because it would be misleading for stable content; the shared
  type is `ContextResponse`. No separate stable-material shape — the section
  name rides in metadata, and a second near-identical type would be a
  synonym.

- **Failure taxonomy: `invalid_request` | `template_error` | `internal_error`.**
  `template_error` is kept distinct because it identifies a config/deployment
  problem in the event log. `normalization_error` is dropped (never
  constructed). `capability.unsupported` remains the mediator-level event for
  unsupported actions.

- **Go request types distinguish missing from empty.** `dynamic_context` and
  `config` are pointers on `RenderRequest`; nil is `invalid_request`, while
  an empty candidate list and a zero config are valid inputs.

#### Implementation Choices (tentative)

- **Package:** `internal/contextbuilder` becomes `internal/contextrenderer`;
  capability string `context_renderer`, contract version
  `context_renderer.v0.3`.

- **Template:** fixed const `text/template` parsed once at init. Opener
  ("You are a helpful assistant.") + material sections rendered as
  `<name>content</name>` blocks in config order + `<available_tools>` block
  in catalog order. No map iteration anywhere, so byte-stability falls out
  for free. "Configured template" flexibility is the capability's, not a v0
  mechanism.

- **Dynamic candidate placement:** rendered into the last user message, in
  provider order, each as `<candidate id="...">content</candidate>` inside a
  clearly-delimited block. No slot grouping. No user message in the
  transcript → nothing injected. Empty candidate list renders nothing.

- **`failures` from the dynamic response are ignored in v0.** The contract
  names candidates as render material; failures reach the model through the
  event record, which the event-model pass will define.

- **Snapshot delivery mechanics** (change gating, supersede text, cleared
  markers) are renderer implementation policy per the contract, not
  contractual. The reference implementation does not implement them in v0:
  it renders the complete current `ContextResponse` each call.

- **No retry wrapper around `render` in the kernel.** Render is deterministic
  and cheap; none of its failure codes are retryable.

#### Open Questions

- **Zero-valued `config`.** A config with empty `Material` is valid but
  degenerate — the prompt collapses to opener + tools. Whether "missing
  config" should be distinguishable from "empty config" at the pairing level
  is revisited when the runtime-kernel contract lands.

- **Material loading may move into the renderer.** A future version may let
  the renderer itself source stable material rather than receiving it in
  `config`. The contract names the slot, not the producer, so this stays
  open.

- **Normalization redo.** The reference's repair transforms are v0-grade. A
  later pass redefines them well — the boundary between harness-side
  normalization and provider-side normalization (e.g. role alternation) may
  shift as more providers are integrated.

- **`system_prompt_id` artifact store resolution.** The contract defines the
  ID as a content hash but not where prompts are stored for later retrieval.
  An artifact store integration is deferred to later contract versions.

### Superseded Directions (builder era)

These entries were accepted for `context_builder.v0`/`v0.1` and are
superseded by the v0.3 collapse. They are preserved so the reasoning is not
lost.

- **Estimate first, then assemble, then prepare** — superseded: one action,
  caller sizes inputs.
- **Two separate surfaces (session-scoped assemble, turn-scoped prepare)** —
  superseded: one action; the caller holds config.
- **`BuiltPrefix` as an explicit cached object** — superseded: the prompt is
  derived per render from config; no kernel-side cache object.
- **Allocation as a self-improvement knob** — superseded: `estimate` is gone;
  sizing belongs to the caller and may become a knob elsewhere.
- **ToolCatalog on `assemble` only, not `prepare`** — superseded: the catalog
  lives inside config, supplied to every render.
- **Bucket-and-slot placement** — superseded: slots are gone from the
  provider contract; placement is the renderer's template policy.
- **Normalization notes as event-level metadata** — superseded in effect: the
  notes are deferred to the event-model pass; the transforms remain.

Retained from the builder era and still load-bearing: template-as-config,
`system_prompt_id` as SHA-256 truncated to 16 hex, catalog ordering owned by
Tool Invocation, passivity, compression as a separate capability, and the
contract-implementation drift rule (agents must surface drift after
implementation).