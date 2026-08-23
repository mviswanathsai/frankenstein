# Context Renderer Capability Contract

Date: 2026-08-23

Contract version: `context_renderer.v0.3`.

Status: draft.

This document defines what the rest of the harness may expect from a Context
Renderer service. It is a capability contract, not an HTTP API, database
schema, or implementation plan.

Design evidence and boundary reasoning live in
`docs/context-builder-service-dossier.md` (file rename pending). Research
into existing systems lives in `docs/research/context-builder-patterns.md`.

`context_renderer.v0.3` is the rename and collapse of `context_builder.v0.2`:
`estimate` and `assemble` are gone, and the capability is one action —
`render`. The action derives the system prompt from a session-scoped `config`
slot, normalizes the transcript, and renders the provider's `DynamicContext`
into the model input. Dynamic delivery never enters the system prompt; the
prefix changes only on a deliberate reassembly via `config`, never by
duration. Snapshot delivery mechanics (change gating, supersede text, cleared
markers) are not contractual; they are renderer implementation policy.

## Purpose

Context Renderer is the single point of contact for "what the model sees." It
has one action: render a per-turn `ModelInput` from the session transcript,
the current dynamic context, and the session-scoped `config`.

The service:

- derives the system prompt and its content-derived identifier from `config`
- normalizes the session transcript into model-facing messages
- renders the dynamic context into model messages per its template policy
- produces a `ModelInput` ready for Model Invocation, with a
  `system_prompt_id` for observability

The renderer is passive: it does not call other capabilities, schedule work,
or decide when to act. The caller (the Runtime Kernel in v0) owns
orchestration.

## Boundary

Context Renderer owns what the model sees: system prompt derivation, message
normalization, and dynamic-context delivery into the model input.

It does not own:

- the session transcript or its persistence. Those belong to Session.
- dynamic context discovery, loading, or content. Those belong to Context
  Provider, which supplies the complete current `DynamicContext` each call.
- stable context production. Agent identity, configured instructions, skill
  indexes, and tool guidance are loaded by the runtime at startup and arrive
  inside `config`; the renderer consumes them, it does not discover them.
- tool definition, availability, or catalog construction. Those belong to
  Tool Invocation.
- model invocation, provider encoding, or provider-specific constraints.
  Those belong to Model Invocation.
- compression, summarization, or transcript state transforms. Those belong
  to Compression (a separate capability, not yet contracted).
- turn lifecycle, retry, fallback, or cancellation. Those belong to the
  Runtime Kernel.
- context-window allocation. `estimate` is gone; the caller sizes the
  transcript and dynamic context before calling `render`.

Context Renderer consumes sized inputs produced by other capabilities. It
must not call those capabilities directly. When a catalog, transcript, or
dynamic context must be trimmed to fit a budget, the caller trims it before
handing it to the renderer. The renderer never mutates its inputs;
transformations happen only in the `ModelInput` it returns.

## Required Action

Each action is carried in the project-wide command envelope. The successful
output shown below is the payload of that action's success event. A
direct-call mediator may return the same terminal event to the caller.

### `context_renderer.render`

Render a per-turn `ModelInput` from the transcript, the current dynamic
context, and the session-scoped `config`.

Input:

```text
RenderRequest {
  id
  session_id?
  transcript: SessionRecord[]
  dynamic_context: DynamicContext
  config
}
```

`id` is required and identifies this request. Terminal payloads refer to it
as `request_id`.

`session_id` identifies the current session when known. Correlation only;
receiving it grants no capability access.

`transcript` is required and must be non-empty. It is the materialized
session transcript from Session, trimmed by the caller to its budget. The
renderer normalizes it into model-facing messages: drops scaffolding and
errored turns, synthesizes missing tool results, and converts internal
markers to model-readable text. Structured recording of these transforms is
deferred to the event-model pass; it is not part of this contract's output.

`dynamic_context` is required. It is the provider's current `DynamicContext`
from `context_provider.v0.2` — `{request_id, candidates, failures}`. An
empty candidate list is valid and renders to no dynamic messages. Candidate
content is material, not binding text: the renderer may preserve, transform,
or omit it per its template policy, consistent with the provider contract's
candidate handling. The renderer renders the candidates into the model
messages; dynamic content never enters the system prompt.

`config` is required. It is the session-scoped material slot, carrying
everything the renderer needs that is stable across turns: stable material
(agent identity, configured instructions, skill indexes, tool guidance), the
tool catalog, model identity, and tool guidance. Its internal structure is
pairing policy between the caller and the renderer implementation, not part
of this contract — the contract requires the slot's presence, not its shape.
The caller deliberately supplies a changed `config` only on a deliberate
reassembly (model switch, configuration change, or an explicit kernel
decision); the renderer treats `config` as an input, never as state it
mutates.

Successful terminal output:

```text
RenderResult {
  request_id
  input: ModelInput
  system_prompt_id
}
```

`input` is the rendered `ModelInput` ready for Model Invocation. `system` is
the system prompt derived from `config`; `messages` is the normalized
transcript plus the rendered dynamic-context messages.

`system_prompt_id` is a content-derived identifier for `input.system`. The
caller may include it in invocation events for observability. It is a
SHA-256 hash of the full `system` text, truncated to 16 hex characters.
Identical `config` always produces the same ID.

Terminal events:

- `context_renderer.rendered`

## The System Prompt

The system prompt is derived from `config` through a configured template. The
template is an implementation detail, not part of this contract. The contract
guarantees only:

- the prompt is a deterministic function of `config`: identical `config`
  always produces an identical prompt
- the prompt carries a content-derived ID for observability
- the renderer is the sole producer of `ModelInput.system`

Template structure, block ordering, formatting, and variable interpolation
are renderer implementation choices. Swapping the renderer changes the prompt
template; the harness treats the prompt as an opaque string with a stable ID.

## Invariants

- `render` is deterministic over its named inputs: identical `transcript`,
  `dynamic_context`, and `config` produce an identical `ModelInput` and
  `system_prompt_id`. The renderer may internally cache or keep derived
  state keyed by inputs; none of it may make outputs vary for identical
  inputs.
- The system prompt and `system_prompt_id` are a deterministic function of
  `config` alone. They do not vary with `transcript`, `dynamic_context`,
  turn count, or elapsed time. Byte-stability is the corollary.
- Dynamic delivery never enters the system prompt. The `dynamic_context`
  renders into the model messages; it never contributes to
  `ModelInput.system`. `render` is the sole producer of `ModelInput.system`.
- Dynamic-context content is material, not binding text. The renderer may
  preserve, transform, or omit it per its template policy, consistent with
  `context_provider.v0.2`'s candidate handling.
- `render` does not mutate its inputs. The caller's `transcript`,
  `dynamic_context`, and `config` are left untouched; transforms happen only
  in the produced `ModelInput`.
- The renderer is passive: it does not call other capabilities, does not
  persist session or external state, and does not decide when to act. Its
  only externally observable effects are its terminal events. Internal
  caching is private, not external persistence.
- `system_prompt_id` is a SHA-256 hash of the full `system` text, truncated
  to 16 hex characters.

## Failure Semantics

Expected failures:

- missing `id` fails as `invalid_request`
- missing or empty `transcript` on `render` fails as `invalid_request`
- missing `dynamic_context` fails as `invalid_request` (an empty candidate
  list is valid, not a failure)
- missing `config` fails as `invalid_request` (its internal shape is not
  validated by this contract)
- an unsupported action returns the project-wide `capability.unsupported`
  terminal event

Not enumerated: missing `model` (it lives inside the unpinned `config`),
malformed `config` (a pairing/deployment concern, not a contract failure),
and unrenderable candidate content (the renderer may transform or omit it).

The renderer does not own recovery from context overflow, compression
triggering, or budget exhaustion. Those are kernel decisions.

## Replay

Replay of a recorded action returns the recorded terminal event. The
renderer is never re-invoked on replay.

## Reuse

This contract reuses:

- `DynamicContext`, `ContextCandidate`, `ContextFailure`, `ContextRef` from
  `context_provider.v0.2`
- `ToolCatalog`, `ToolDefinition` from `tool_invocation.v0`
- `SessionRecord` from `session.v0.3`
- `ModelInput`, `ModelMessage`, `ModelMessageRole` from
  `model_invocation.v0`

It does not redefine them. Context Renderer consumes `DynamicContext` (on
`render`), consumes `SessionRecord`, produces `ModelInput`, and reads the
tool catalog (inside `config`) for tool awareness text in the system prompt.

Stable material is produced by the runtime from the loaded agent
configuration and arrives inside `config`; this contract names the slot, not
the producer. The runtime-kernel contract, when drafted, will own production.

`session_id` follows the same correlation convention as the other contracts:
it identifies the current session when known, and receiving it grants no
capability access.

Terminal payloads echo the request `id` as `request_id`, as in the other
contracts.

Unsupported actions return the project-wide `capability.unsupported`
terminal event.