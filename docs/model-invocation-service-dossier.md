# Model Invocation Service Dossier

Date: 2026-08-25

Contract version: `model_invocation.v0.1`.

Status: draft.

Contract draft: `docs/model-invocation-capability-contract.md`.

This dossier records the evidence and boundary decisions behind the Model
Invocation capability. It is descriptive working material. The contract is the
stable surface. It was revised alongside the contract's v0.1 rewrite, which
followed a reality check of the v0 contract against the runtime-kernel
implementation.

## Working Conclusion

Model Invocation is the harness's single point of contact with LLM providers.
It has one action, `model_invocation.invoke`, which performs one model call
against assembled provider-neutral input (`ModelInput`) plus the canonical
`ToolCatalog`, and returns the next assistant turn: content, opaque reasoning
evidence, complete canonical tool calls, a typed `outcome`, usage, and
provenance. Failures are typed payloads with retryable classification.

It does not own:

- model-input layout — the context renderer owns it; its render action
  produces the `ModelInput`
- tool execution or authoritative tool resolution (Tool Invocation)
- retry, backoff, fallback, or continuation (Runtime Kernel, contract not yet
  drafted)
- session records (Session)
- turn lifecycle, budgets, or cancellation (Runtime Kernel)

One invoke is one provider call attempt: there are no internal retries.
Streaming may surface as progressive observations, which are transport: they
never enter the event log, and the terminal event alone is authoritative.
Interpretive repair of model output — malformed argument JSON and mangled
tool names against the supplied catalog — lives here. Authoritative
resolution stays in Tool Invocation. Repair never grants authority.

Reasoning evidence is owned per layer: intermediate layers carry it
opaquely, adapters decide what reaches the wire. See the Discussion Record
for the evidence-ownership decision and the provider fact that motivated
it.

## Capability

Model Invocation.

## User-Visible Job

Call the model and return what it said and what it asked for, in a normalized
and complete form, honestly.

From a user's point of view, this includes:

- the model is actually called with the assembled input
- responses arrive in a usable normalized form regardless of provider
- the model's tool requests survive sloppy spelling and malformed argument
  JSON when repair is safe
- truncation, provider errors, and empty responses are reported honestly
  instead of being papered over
- cost is recorded as usage
- what was sent and what came back remain explainable afterward

Swapping this capability changes provider coverage, streaming philosophy, how
much model sloppiness the harness repairs and tolerates, and how provider
failures are surfaced.

## Runtime Job

The capability:

- accepts one provider-neutral `ModelInput` and one canonical `ToolCatalog`
- encodes both into the selected provider's wire format
- performs exactly one provider call attempt
- streams internally, accumulating fragments into complete content and
  complete tool calls; may surface progress as non-normative observations
- maps provider-native tool names back to canonical names and attaches
  `tool_id` and `definition_revision` from the supplied catalog
- interpretively repairs malformed argument JSON and mangled tool names
  against the supplied catalog
- normalizes usage, outcome, and provider errors into typed contract shapes;
  passes the provider's finish reason through verbatim
- reports malformed or incomplete provider output to the Runtime Kernel
  instead of executing, dropping, or faking it

The Runtime Kernel remains responsible for deciding when the model is called
again, retrying, falling back, and ending the turn.

## Hermes Evidence

Evidence observed in `/home/mviswanathsai/hermes-agent`.

### The Loop Anti-Pattern

Relevant file: `agent/conversation_loop.py`.

- `run_conversation` is one function of roughly 4900 lines on a mutable
  `AIAgent`.
- Context assembly, compression decisions, persistence cadence,
  retry/backoff/fallback, error classification, tool-call validation, usage
  accounting, and policy gates are all inline.
- The Model Invocation / Runtime Kernel split exists to prevent this: the
  adapter owns one call, the kernel owns the loop.

### The Transport Seam

Relevant files: `agent/transports/base.py`, `agent/transports/types.py`.

- `ProviderTransport` is the clean seam: `convert_messages` → `convert_tools`
  → `build_kwargs` → `normalize_response`.
- Its docstring explicitly disowns retry, streaming presentation, and
  interrupts.
- `NormalizedResponse` and `ToolCall` are the basis for our normalized output
  shapes.

### Argument Repair

Relevant file: `agent/message_sanitization.py`.

- `_repair_tool_call_arguments` runs escalating passes: loose parse, trailing
  commas, brace balancing, and a character-escaping state machine.
- The last resort is `{}` — better than crashing the session.
- The most common local-model case is literal control characters inside
  strings.

### Name Repair

Relevant file: `agent/agent_runtime_helpers.py`.

- `repair_tool_call` normalizes tool names: lowercase, hyphen and space to
  underscore, camelCase to snake_case, strip the `_tool` suffix, then difflib
  fuzzy matching with a 0.7 cutoff.
- After repair, errors are fed back to the model as tool results up to 3
  times. In our split that feedback policy is kernel work, not adapter work.

### Truncation

- `finish_reason=length` is the ordinary truncation signal, but some streams
  (NVIDIA Nemotron) stall without any finish reason.
- Some routers rewrite `length` to `tool_calls`, hiding truncation.
- Truncated arguments are refused rather than repaired into executable calls.

### Empty And Thinking-Only Responses

- Hermes answers empty and thinking-only responses with nudges, prefill
  continuation, 3 retries, and fallback.
- These are loop-policy decisions, not adapter mechanics — evidence that they
  belong to the kernel.

### Streaming Quirks

- MiniMax resends the full tool name in every chunk; naive concatenation
  yields `read_fileread_file`.
- Ollama reuses chunk indexes.
- Some providers emit integer call IDs that must be coerced to strings.

### Provider Variance

- 5 `api_modes` and 31 declarative provider profiles.
- Field surgery per provider: Codex field stripping, Gemini-only
  `extra_content`, Anthropic `cache_control` breakpoints, DeepSeek/Kimi
  reasoning echo-back padding.
- Codex alone needs three distinct paths.
- The most unstable provider bypasses the OpenAI SDK entirely with raw SSE
  because the SDK's payload shapes drifted.

The DeepSeek/Kimi line is the seed of the v0.1 evidence-ownership decision:
reasoning echo-back is per-provider wire policy, not a harness-wide rule.
Chat-completions-family providers reject echoed-back reasoning outright
(their API returns 400 when `reasoning_content` appears in input), while
Anthropic-style providers require signed evidence blocks to be returned.
No harness-level obligation can span both; only an adapter knows which
world it is in.

### Error Taxonomy

Relevant file: `agent/error_classifier.py`.

- `ClassifiedError` carries `retryable`, `should_compress`,
  `should_rotate_credential`, and `should_fallback`.
- It is the basis for our failure codes, with every decision it names moved to
  the kernel.

### Turn Exit Reasons

- Every turn exit records an exit-reason string.
- This is evidence for typed termination in the future kernel contract.

## Pi Evidence

Evidence observed in `/home/mviswanathsai/pi`.

Relevant files: `packages/agent/src/agent-loop.ts`, `packages/ai`.

- The agent loop is config-driven over a uniform `StreamFunction`.
- Errors are encoded as `stopReason` values `"error"` or `"aborted"` and never
  thrown. We chose typed failure events instead — see the Discussion Record
  for why the divergence is deliberate.
- Streaming is always on, and `AssistantMessage` carries usage, diagnostics,
  and stop reason — the "the response is the record" pattern.
- The loop performs no retries; the session layer does. This supports retry
  ownership in the kernel.
- There is no iteration budget anywhere. Negative evidence: the kernel
  contract must have one.
- The Agent object owns the transcript while the loop works on a snapshot.
  This supports session-owns-records with kernel materialization.

Further loop observations from the v0.1 review:

- `StopReason = "stop" | "length" | "toolUse" | "error" | "aborted"` mixes
  two kinds of information: what the payload already shows (toolUse vs
  stop) and what it cannot show (length, error, aborted). Our v0.1 split
  follows the same fault line: `tool_calls` presence carries the first
  kind; typed `outcome` plus failure codes carry the second.
- A `length`-stopped message poisons its whole tool-call batch: the loop
  fails every call from that message instead of executing any, because
  argument strings from a cut-off response may be borked even when they
  parse. This is field evidence for distrusting repair-fabricated
  arguments. In our contract this stays an implementation policy, not a
  rule: the primitives (`outcome`, per-call repair notes) let an
  implementation make that choice.
- A text-only `length` stop simply ends the run with a visible truncation
  notice — no auto-continue, no special exit code. Confirms that outcome
  informs kernel policy rather than encoding it.
- Pi's retry logic reads `errorMessage` strings and approximates with
  `stopReason !== "stop"` — the cost of untyped failure evidence inside a
  single shape. Positive evidence for our typed failure payloads.
- Pi's `Usage` includes a full provider-cost breakdown. Deferred for us;
  when pillar 3 asks "optimize for what?", cost-per-turn is the obvious
  first metric and usage is where it lands.

## State Owned Or Mutated

A Model Invocation service may own:

- provider adapter configuration and provider profiles
- the provider-encoded request payload for the current call
- in-flight call state, including streamed fragment accumulation
- repair state for malformed tool-call arguments and names

It does not own:

- `ModelInput` assembly (the context renderer)
- canonical tool definitions or catalogs (Tool Invocation)
- tool execution, policy, approval, or authoritative resolution (Tool
  Invocation)
- retry, backoff, or fallback state (Runtime Kernel)
- session records or session-level usage accounting (Session; Model Invocation
  only reports per-call usage)
- turn budgets, cancellation authority, or the decision to continue (Runtime
  Kernel)

## Inputs

Observed inputs include:

- `ModelInput`, the provider-neutral assembled model input; this is the
  context renderer's output shape, defined in the Model Invocation contract
- the canonical `ToolCatalog` the model call should see
- provider and model selection, plus optional generation parameters
- `session_id`, `turn_id`, and the request's own `id` for causality
- cancellation and deadline signals carried by the mediator

Model input arrives assembled. Model Invocation does not read the session,
call Context Provider, or fetch a catalog itself.

## Outputs

Observed outputs include:

- normalized content and opaque reasoning evidence attached to the turn
- complete canonical `ToolCall` values with `tool_id` and
  `definition_revision` attached from the supplied catalog
- typed `outcome`, plus the provider's finish reason passed through
  verbatim and advisory labels for finer classification
- usage, reusing `TokenCount` for token values
- typed failures with retryable classification
- repair evidence for interpreted tool-call arguments and names, including
  calls dropped to truncation
- provenance: request echo, catalog ID used, model that actually ran,
  provider response id when the adapter surfaces one

## External Effects

- network calls to the configured model provider: one request and its stream
  reads per invoke

No file writes, no state mutation outside the capability, and no calls into
other capabilities. Model Invocation is a consumer-facing network boundary;
its only side effect is one provider call attempt.

## Failure Modes

Expected failures include:

- provider transport failure: connect errors, timeouts, mid-stream disconnect
- malformed provider output that cannot be normalized
- truncation, either a length finish reason or a stream that stalls without
  one
- empty or thinking-only responses
- authentication or credential failure
- rate limiting or overload
- content filtering
- cancellation
- tool calls that cannot be repaired against the supplied catalog

## Recovery Behavior

Required recovery principles:

- no internal retries: one invoke is one provider call attempt; retry,
  backoff, and fallback are kernel decisions made from typed failure evidence
- cancellation reports the `cancelled` failure code; pre-cancellation text
  has already reached observers through the observation channel where one
  exists, so the failure payload carries no duplicate copy
- a truncated response is reported as truncated; truncated tool-call
  fragments are never emitted as calls — they survive only as repair notes
- repair is interpretive only: repaired names and arguments still pass through
  Tool Invocation resolution and validation
- malformed provider output that cannot be normalized is reported to the
  Runtime Kernel, not dropped and not faked
- replay reads recorded model-input evidence and terminal events; it never
  calls the provider again

## Hidden Coupling To Avoid

- letting the adapter own retry, fallback, continuation, or compression
  decisions (the Hermes loop anti-pattern)
- letting repair grant authority: repaired names and arguments still require
  Tool Invocation resolution and validation
- treating provider-native tool names, call IDs, or response-item IDs as
  canonical identity
- letting streaming mechanics become semantic events; observations are
  transport and the terminal event alone is authoritative
- consumers branching on advisory fields (`finish_reason`, `labels`,
  `repairs`); a value that starts driving behavior must be promoted into
  the typed decision vocabulary by a contract version bump
- assuming one reasoning echo-back rule spans providers; wire policy for
  evidence is adapter knowledge
- letting Model Invocation assemble or modify model input (the renderer's
  role)
- hiding truncation, including router rewrites of `length` to `tool_calls`
- depending on provider SDK payload shapes as a stable interface
- feeding repair feedback to the model from inside the adapter; that is kernel
  policy

## Possible Alternate Philosophies

- strict pass-through that fails on any malformed model output
- interpretive repair with recorded evidence (the selected reference
  direction)
- always-streaming versus streaming chosen per request
- provider SDK integration versus raw HTTP and SSE
- a thin decoder that returns provider shapes versus a thick normalizer that
  also classifies errors
- a single-provider adapter versus a multi-provider normalizer
- failures as turn-shaped results in one unified shape (Pi's way) versus
  typed failure payloads (the selected direction)

These choices are meaningful enough that Model Invocation deserves a
capability contract.

## Language Decision

Go for Model Invocation, and for the future Runtime Kernel.

Evidence:

- Every repair found in Hermes is string-level — regex, brace counting, and
  character-escaping state machines — and ports line-for-line.
- Fuzzy name repair is string-to-string before strict parsing, so static
  typing never sees malformed JSON.
- Provider SDK dependence is thin and shown fragile: Hermes's pyproject notes
  a pydantic-core segfault, and Codex streaming bypasses the SDK. HTTP plus
  SSE plus `encoding/json` suffices.

This deliberately revises the AGENTS.md stance "Python for provider adapters".
Memory extraction and rich tools may keep their Python claim. The project
should record this stance revision.

## Reconciliation With Existing Contracts

- `ToolCall` and `ToolCatalog` are reused from `tool_invocation.v0`.
- `TokenCount` is reused from `session.v0.3`.
- `session_id`, `turn_id`, and `request_id` conventions are unchanged.
  Moving correlation to envelope-level causality references across all
  contracts is a recorded direction, not a v0.1 change.
- `ModelInput` — now turn-based (`turns: Turn[]`) — is defined in the Model
  Invocation contract and produced by the context renderer. This mirrors
  the `ToolCall` precedent: the consumer contract owns the shape, and the
  producer is named.
- The context renderer contract still uses `messages` vocabulary; it needs
  an alignment pass to the turn-based IR in its next revision.
- No changes are needed to the session, context_provider, or tool_invocation
  contracts. A future session revision should give reasoning evidence an
  explicit home on assistant records so the transcript-to-IR chain carries
  it without informal use of `Raw`.
- Kernel-assigned record IDs make turn-to-record correlation a pure event-log
  query.

## Adjacent: Runtime Kernel

The Runtime Kernel is the next capability to contract, and this dossier
carries its dossier material. The kernel owns:

- turn lifecycle and the model-call sequence
- budgets and global cancellation
- continue/stop decisions
- retry, backoff, and fallback policy
- `catalog_transition` handling between calls
- honoring `ToolResult.stop_requested`
- appending session records with caller-assigned record IDs
- materializing assistant turns into the session, including reasoning
  evidence so later renders can carry it forward
- empty and thinking-only response policy: the service returns the turn
  as-is (`outcome: complete`, empty content); the kernel decides whether to
  nudge, retry, fall back, or accept the turn as finished. Hermes runs a
  recovery ladder (nudge → prefill → retry → fallback); Pi treats empty as
  a normal terminal state. Both approaches are valid kernel policies.
- what to do with an incomplete text-only turn (`outcome: max_output`):
  end it, re-invoke with a continuation nudge, or compress and retry —
  all expressible from the contract's primitives; none mandated by them

Termination should be typed. Hermes's exit-reason strings are positive
evidence; Pi's missing iteration budget is negative evidence that the kernel
contract must define budgets and typed exits.

The kernel dossier and contract are the next work item.

## Discussion Record

This section preserves design and implementation discussion across working
sessions. It is not normative. Accepted entries are directions for later
contract reconciliation, not changes to the current draft by themselves.

### v0.1 Revision Note (2026-08-25)

The v0 contract was checked against the runtime-kernel implementation
(`internal/kernel`, `internal/modelinvocation`, `internal/modelinvocation/openai`).
The check found real bugs (the kernel never sets `provider`; the
truncation stop-reason override violated the stated invariant), confirmed
most of the surface, and triggered the v0.1 rewrite recorded below. The
attitude for this pass, set by Viswa: the implementation is a faulty v0,
not ground truth — use it for usage patterns and ergonomics, judge shapes
on their own terms.

### Accepted Directions

Pre-v0.1 directions that still stand:

- Split Model Invocation from the Runtime Kernel.
- Contract Model Invocation first.
- Go for Model Invocation and the future kernel.
- Accumulation is provider-interleaved: the adapter delivers normalized
  fragments, and the service owns the loop that accumulates and repairs.
  Provider-specific quirks — name deduplication, index reuse, integer
  coercion — are decode concerns in the adapter. Model-output repair —
  argument JSON and name matching — is provider-agnostic and runs in the
  service after accumulation.
- Interpretive name repair lives in Model Invocation; authoritative resolution
  lives in Tool Invocation.
- No internal retries: one invoke is one provider call attempt.
- Provider is a required field on the request. The kernel supplies both
  provider and model; the service dispatches to the configured adapter. No
  model-to-provider resolution lives inside the service.
- Empty responses (no content, no tool calls) are valid successful turns.
  The service returns them as-is; deciding whether to nudge, retry, fall
  back, or accept the turn as finished is kernel policy.
- Tool-call ID generation is a sequential index within the result (`"0"`,
  `"1"`, ...). `ToolCall.id` correlates a call to its `ToolResult` via
  `call_id`; tool identity is the `tool_id` resolved from the catalog.
- Turn correlation via envelope causality in the event log, with
  kernel-assigned record IDs.

Accepted in the v0.1 revision:

- **Turn-shaped payload.** The success payload is the assistant turn plus
  provenance, not a response object. The dataflow closes: session stores
  turns, renderer compiles turns into `ModelInput`, invoke returns the
  next turn. Provenance fields do not project into the transcript.
- **Reasoning evidence ownership per layer** (supersedes the v0 echo-back
  direction). Evidence is typed `{ kind, data }`, carried opaquely by
  intermediate layers; each adapter owns wire policy — encode it back,
  filter it out, or fail loudly rather than corrupt the conversation.
  Motivating fact: chat-completions-family providers (DeepSeek among
  them) reject echoed-back reasoning with a 400, while Anthropic-style
  providers require signed blocks returned. No harness-wide rule spans
  both. Hermes's "DeepSeek/Kimi reasoning echo-back padding" was the seed;
  the v0 implementation encoded evidence into input messages, which would
  break against DeepSeek if ever exercised.
- **Decision vocabulary is exactly what consumers branch on.** The full
  branch inventory for this capability's output: tool-call presence
  (continue?), `outcome` (trust/end semantics of a text-only turn),
  failure code + retryable (remedy selection), usage against thresholds
  (budgets), `Evidence.kind` (adapter encode/filter/fail), observation
  deltas (UI progress). Everything else is read-only. Closed vocabularies
  only exist where branching happens; adding a value requires a version
  bump.
- **`stop` removed; replaced by `outcome` + passthrough.** The old enum
  mixed derivable facts (`end_turn`, `tool_calls` — readable from the
  payload) with non-derivable ones (`max_output`, `content_filter`). The
  first kind died; the second became `outcome`. The provider's raw finish
  reason passes through verbatim as `finish_reason`, never normalized
  (the v0 implementation silently coerced unknown reasons to end_turn —
  the failure mode enums invite).
- **Advisory channels for what nobody branches on.** `labels`
  (open-keyed, non-normative) and verbatim `finish_reason`. This is not a
  statement of unimportance but the guarantee that makes them extensible:
  new keys need no version churn because correctness cannot depend on
  them. Promotion rule: the moment any consumer branches on an advisory
  value, it must be promoted into typed vocabulary by a version bump.
- **Failures stay a separate typed payload** (deliberate divergence from
  Pi). Pi encodes errors as assistant messages (`stopReason:
  "error"/"aborted"`); its retry logic then approximates from
  `errorMessage` strings. Our remedies need typed codes
  (`rate_limited` → backoff, `context_overflow` → compress), and dual
  return is the project-wide convention.
- **Truncated call fragments are never emitted; they survive as repair
  notes** carrying the would-be id and the partial accumulated name, so
  downstream consumers can say which tool was being called. Whether an
  implementation additionally refuses the whole batch from an interrupted
  response — Pi fails every call in a `length`-stopped message, since
  brace-balancing can fabricate parseable-but-wrong arguments — is that
  implementation's policy, expressible from `outcome` plus repairs.
- **Streaming observations are transport-grade contract surface.** They
  carry no ordering guarantee, never enter the event log, impose no
  replay obligation; adapters that cannot stream deliver nothing
  progressively. This makes the kernel's existing observer shape honest
  without promoting deltas to semantic events.
- **`GenParams` gives generation parameters a named home**, starting with
  `max_output_tokens`; absent means default. Parameters join the shape,
  not the request, so the extension point exists before the second
  parameter forces an ad hoc one.
- **Provenance kept despite stub-era non-use:** `model` (what actually
  ran) and `provider_response_id` become load-bearing the day fallback
  routing exists; the record must say what ran, not what was asked.
- **Correlation stays in the request payload for v0.1** (consistent with
  all other contracts); envelope-level causality remains the direction.

Superseded pre-v0.1 directions, kept for the record:

- ~~Streaming internal, terminal-event-only, partial text preserved on
  cancellation~~ — replaced by declared observations; the `partial`
  failure field was cut because observers already received the bytes.
- ~~Structural truncation forces the stop reason to `max_output` as a
  corrective signal~~ — replaced by structural dropping plus repair
  notes; the service does not rewrite evidence.

### Tentative Implementation Choices

- A `RepairNote` shape with kinds `name | arguments | truncated`; raw
  argument text deferred.
- An empty-object fallback for unrepairable arguments, with Tool
  Invocation's schema validation as the backstop.
- Deriving `outcome` from the provider finish reason, with structural
  truncation evidence allowed to override toward `max_output` where the
  provider's claim is untrustworthy (routers rewrite `length`;
  Nemotron-class streams stall without any reason).
- Sequential result-local tool-call ids as above.

### Open Questions

- How provider-hosted tools and their effects are represented; the tool
  contract says to represent them in Model Invocation events as
  provider-owned effects.
- Cache-control placement coordination between the renderer and Model
  Invocation.
- Cancellation mechanics, pending a runtime/mediator contract.
- Reasoning-effort and thinking-level control; natural future residents of
  `GenParams`.
- Whether and how pre-repair raw argument text is recorded, given size and
  content sensitivity.
- An explicit session-record slot for reasoning evidence, so the
  transcript-to-IR-to-adapter chain carries it without informal use of
  `Raw`.
- Provider cost breakdown in `Usage` (Pi carries full cost data); lands
  when pillar 3 names cost as a target metric.
- A cataloged convention list for `labels` (first candidate:
  adapter-emitted finish classification such as `finish_class`) once a
  second consumer appears.
- Whether `session.record_written` should name appended record IDs, or whether the
  caller-assigned convention suffices.
