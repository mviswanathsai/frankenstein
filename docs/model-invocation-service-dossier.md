# Model Invocation Service Dossier

Date: 2026-08-07

Contract version: `model_invocation.v0`.

Status: draft.

Contract draft: `docs/model-invocation-capability-contract.md`.

This dossier records the evidence and boundary decisions behind the Model
Invocation capability. It is descriptive working material. The contract is the
stable surface.

## Working Conclusion

Model Invocation is the harness's single point of contact with LLM providers.
It has one action, `model_invocation.invoke`, which performs one model call
against assembled provider-neutral input (`ModelInput`) plus the canonical
`ToolCatalog`, and returns normalized content, complete canonical tool calls,
usage, stop reason, and typed failures.

It does not own:

- model-input layout — an uncontracted "builder" role that the Runtime Kernel
  plays in v0
- tool execution or authoritative tool resolution (Tool Invocation)
- retry, backoff, fallback, or continuation (Runtime Kernel, contract not yet
  drafted)
- session records (Session)
- turn lifecycle, budgets, or cancellation (Runtime Kernel)

One invoke is one provider call attempt: there are no internal retries.
Streaming is an internal mechanic; v0 is terminal-event-only. Cancellation
preserves partial text; transport death mid-stream discards it. Interpretive
repair of model output — malformed argument JSON and mangled tool names against
the supplied catalog — lives here. Authoritative resolution stays in Tool
Invocation. Repair never grants authority.

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
- streams internally and accumulates fragments into complete content and
  complete tool calls
- maps provider-native tool names back to canonical names and attaches
  `tool_id` and `definition_revision` from the supplied catalog
- interpretively repairs malformed argument JSON and mangled tool names
  against the supplied catalog
- normalizes usage, stop reason, and provider errors into typed contract
  shapes
- reports malformed or incomplete provider output to the Runtime Kernel
  instead of executing, dropping, or faking it

The Runtime Kernel remains responsible for assembling the input, deciding when
the model is called again, retrying, falling back, and ending the turn.

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
  thrown. We chose typed failure events instead.
- Streaming is always on, and `AssistantMessage` carries usage, diagnostics,
  and stop reason — the "the response is the record" pattern.
- The loop performs no retries; the session layer does. This supports retry
  ownership in the kernel.
- There is no iteration budget anywhere. Negative evidence: the kernel
  contract must have one.
- The Agent object owns the transcript while the loop works on a snapshot.
  This supports session-owns-records with kernel materialization.

## State Owned Or Mutated

A Model Invocation service may own:

- provider adapter configuration and provider profiles
- the provider-encoded request payload for the current call
- in-flight call state, including streamed fragment accumulation
- repair state for malformed tool-call arguments and names
- the provider-encoded tool bundle, or a durable reference to it, retained as
  model-input replay evidence

It does not own:

- `ModelInput` assembly (the builder role)
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
  builder's output shape, defined in the Model Invocation contract
- the canonical `ToolCatalog` the model call should see
- provider and model selection
- `session_id`, `turn_id`, and the request's own `id` for causality
- cancellation and deadline signals carried by the mediator

Model input arrives assembled. Model Invocation does not read the session,
call Context Provider, or fetch a catalog itself.

## Outputs

Observed outputs include:

- normalized content
- complete canonical `ToolCall` values with `tool_id` and
  `definition_revision` attached from the supplied catalog
- usage, reusing `TokenCount` for token values
- stop reason
- typed failures with retryable classification
- repair evidence for interpreted tool-call arguments and names
- provider replay metadata: the exact catalog ID used, and the retained
  provider-encoded bundle or a durable reference to it

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
- cancellation preserves accumulated partial text; transport death mid-stream
  discards it and reports failure
- a truncated response is reported as truncated; truncated tool arguments are
  not submitted as valid calls
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
- letting streaming mechanics become contract surface; v0 is
  terminal-event-only
- letting Model Invocation assemble or modify model input (the builder role)
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
- `TokenCount` is reused from `session.v0.2`.
- `session_id`, `turn_id`, and `request_id` conventions are unchanged.
- `ModelInput`, the builder's output shape, is defined in the Model Invocation
  contract. This mirrors the `ToolCall` precedent: the consumer contract owns
  the shape, and the producer is named.
- No changes are needed to the session, context_provider, or tool_invocation
  contracts.
- Kernel-assigned record IDs make turn-to-record correlation a pure event-log
  query.

## Adjacent: Runtime Kernel

The Runtime Kernel is the next capability to contract, and this dossier
carries its dossier material. The kernel owns:

- `turn_id` issuance, resolving the tool contract's turn_id debt
- turn lifecycle and the model-call sequence
- budgets and global cancellation
- continue/stop decisions
- retry, backoff, and fallback policy
- `catalog_transition` handling between calls
- honoring `ToolResult.stop_requested`
- appending session records with caller-assigned record IDs
- the builder role in v0: assembling `ModelInput` from the `ContextBundle` and
  the materialized session before each invoke
- empty and thinking-only response policy: the service returns the result
  as-is; the kernel decides whether to nudge, retry, fall back, or accept
  the turn as finished. Hermes runs a recovery ladder (nudge → prefill →
  retry → fallback); Pi treats empty as a normal terminal state. Both
  approaches are valid kernel policies.

Termination should be typed. Hermes's exit-reason strings are positive
evidence; Pi's missing iteration budget is negative evidence that the kernel
contract must define budgets and typed exits.

The kernel dossier and contract are the next work item.

## Discussion Record

This section preserves design and implementation discussion across working
sessions. It is not normative. Accepted entries are directions for later
contract reconciliation, not changes to the current draft by themselves.

### Accepted Directions

- Split Model Invocation from the Runtime Kernel.
- Contract Model Invocation first.
- The builder stays uncontracted; its output shape (`ModelInput`) is defined
  in the Model Invocation contract.
- Go for Model Invocation and the future kernel.
- Streaming is internal; v0 is terminal-event-only, with partial text
  preserved on cancellation.
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
- Truncation detection is structural and lives in the service. After
  accumulation and before repair, the service checks whether tool-call
  argument JSON is structurally closed. When the stream ends with
  unclosed JSON — regardless of what the provider's finish reason claims —
  the service emits `stop_reason: max_output`. No new RepairKind or
  recorded-discrepancy field is needed; the honest stop reason is the
  corrective signal. The provider's claim is not actionable to the kernel
  or the model, so it is not preserved.
- Turn correlation via envelope causality in the event log, with
  kernel-assigned record IDs.
- Empty responses (no content, no tool calls, `stop_reason: end_turn`) are
  valid successful results. The service returns them as-is; deciding whether
  to nudge, retry, fall back, or accept the turn as finished is kernel
  policy. No provider signals empty+stop as an error, so no
  provider-specific detection lives in the adapters.
- Tool-call ID generation is a sequential index within the result (`"0"`,
  `"1"`, ...). The `ToolCall.id` exists only to correlate a specific call
  to its `ToolResult` via `call_id` when multiple calls share the same
  `tool_id`. Tool identity is the `tool_id` field resolved from the
  catalog; the `id` field is not involved in catalog matching. The result's
  `request_id`, the execution request's `session_id`, `turn_id`, and
  `catalog_id` provide global scoping — the local index does not need to
  be globally unique.

### Tentative Implementation Choices

- A `RepairNote` shape; raw argument evidence is deferred.
- A reasoning echo-back field.
- A stop_reason enum.
- An empty-object fallback for unrepairable arguments.

### Open Questions

- How provider-hosted tools and their effects are represented; the tool
  contract says to represent them in Model Invocation events as
  provider-owned effects.
- Cache-control placement coordination between the builder and Model
  Invocation.
- Cancellation mechanics, pending a runtime/mediator contract.
- reasoning_effort and thinking-level control.
- How rich raw-form repair evidence should be.
- Whether `session.mutated` should name appended record IDs, or whether the
  caller-assigned convention suffices.
