# Project Status

Where Frankenstein stands, what is being worked on, and what comes next.

This is the project's status surface. The contracts, dossiers, and census are
the source of truth; this document is the pointer map. `AGENTS.md` is the
stable operating document and does not carry this state.

Update this file whenever a tracked item changes:

- a methodology step changes status
- a contract or dossier is drafted, updated, or versioned
- a next step starts or finishes
- a capability area moves into active work

## Methodology Status

The methodology is the pillar-1 build order. See `AGENTS.md` for what each step
means.

1. Hermes census — first pass complete. The census document is pending —
   the analysis was done but the file was never committed. Targeted
   follow-ups remain and are listed here.
2. Capability clustering — partial. Reasoning lives in the census and the
   drafted dossiers.
3. Substitution test — partial. The dossiers record contract-worthy judgments.
   The remaining capability areas need the same treatment.
4. Contract draft — partial. Five contracts drafted, listed below.
5. Control flow — pending. A development scaffold exists in the census; the
   formal control flow depends on the runtime-kernel contract.

## Drafted Contracts

- session — `session.v0.3` — `docs/session-capability-contract.md`
- context provider — `context_provider.v0.2` —
  `docs/context-provider-capability-contract.md` (two read-style actions —
  `get_dynamic_context` per turn, `get_stable_context` once per session — and
  one shared `ContextResponse` shape; Go implementation still v0.1-shaped and
  needs the v0.2 update)
- tool invocation — `tool_invocation.v0` —
  `docs/tool-invocation-capability-contract.md`
- model invocation — `model_invocation.v0.1` —
  `docs/model-invocation-capability-contract.md` (dossier at
  `docs/model-invocation-service-dossier.md`, revised alongside; v0.1
  reshapes the success payload into the assistant turn, types reasoning
  as opaque `Evidence` with adapter-owned wire policy, adds a
  transport-grade streaming observation channel, replaces the stop enum
  with a typed `outcome` plus verbatim `finish_reason` and advisory
  `labels`, and removes the partial-output field and the tool-calls/stop
  coupling invariant; Go implementation still v0-shaped and needs the
  update, including the kernel never setting `provider` on the request)
- context renderer — `context_renderer.v0.3` —
  `docs/context-renderer-capability-contract.md` (renamed and collapsed from
  context builder; dossier at `docs/context-renderer-service-dossier.md`; Go
  implementation still the v0 builder in `internal/contextbuilder/` and needs
  the rework)

Not yet drafted: runtime kernel, memory, compression, and the observability
event model.

## Next Steps

Work in this order. Move each item forward as it lands.

1. Implement `session.v0.3` changes across the Go codebase — complete. The
   `SessionStore` interface now exposes `Create`/`Get`/`Delete` plus the
   dedicated write actions (`WriteMessage`, `WriteToolCall`, `WriteToolResult`,
   `WriteSystemNote`, `WriteRecord`, `SetMetadata`, `SetUsage`); `Resume`,
   `Read`, `Materialize`, and `Mutate` are gone. The kernel loop writes through
   the new actions (user and assistant messages via `WriteMessage`, inner-loop
   tool results via the dedicated `WriteToolResult`/`WriteToolCall` writes, the
   built prefix via `SetMetadata`) and reloads the session with `Get` after
   `Create`. The store infers `turn_id` from the record stream and persists
   `tool_calls` and `call_id` alongside it. The dead `mutate` path is fully
   removed: `session_mutation_results`, `mutationAlreadyApplied`,
   `insertMutationResult`, and `ErrInvalidMutation` are gone. `Seq`, `Raw`,
   `CharCount`, and `Tokens` stay implementation-private on `SessionRecord`
   (`json:"-"`) and no longer leak into `Get` output.
2. Finish the Hermes census follow-ups the drafted contracts already depend on:
   session lineage and replay in `hermes_state.py`, fallback and streaming
   interrupt behavior in `chat_completion_helpers.py`, plugin hooks, approval
   and checkpoint policy, gateway delivery constraints, memory providers, and
   eval/cron paths.
3. Context renderer rework — decided, implementation sequenced. The v0
   builder in `internal/contextbuilder/` (`estimate`, `assemble`, `prepare`)
   is superseded by `context_renderer.v0.3`: one `render` action, a
   session-scoped `config` slot, `ContextResponse` dynamic input. All design
   decisions and pairing policy are recorded in
   `docs/context-renderer-service-dossier.md`. The context provider v0.2 Go
   update is in progress: the flat `ContextResponse` shape, the new
   `get_stable_context` action, `metadata.slot` conventions, the stable-set
   partition, and the `touchedpath` micro-package are being implemented. The
   renderer rework still follows (package `internal/contextrenderer`, single
   `Render`, kernel builds config once per session and holds it,
   `built_prefix` cache removed, no per-turn catalog fetch).
4. Draft the runtime-kernel contract — the coherence point the other contracts
   reach into: turn lifecycle, ordering, budgets, cancellation, recovery, and
   replay invariants. The runtime kernel is the next capability in active
   work: its dossier material is captured in the model-invocation dossier
   ("Adjacent: Runtime Kernel"), and the kernel dossier and contract follow.
5. Define the semantic event model as its own surface. Observability is a
   pillar, so the append-only event log gets a first-class contract with replay
   semantics.
6. Draft memory and compression as state transforms, with provenance and
   non-blocking extraction semantics.
7. Draft what a target slot looks like, even as a stub. Pin the shape of the
   objective surface before the harness optimizes against it.
8. Build the first reference composition: direct mediator calls, primary and
   shadow services, and a replayable event log.

## Open Problems

- **Introducing entropy** — where the non-obvious improvements come from.
  Compounding wins are found in use, not designed in, and many begin as
  cross-domain leaps that models undergenerate by design. The harness should
  keep room for metered, weakly-related candidates; shadow services already
  give the shape, and the event log provides the evidence. The open questions
  are candidate generation, the entropy budget, and the long evaluation
  horizon that lets a sidequest earn promotion. Runs as an active research
  arena: observations and trials accumulate in `docs/entropy-research.md`.
  See the note in `AGENTS.md`.

## Candidate Capability Areas

The service slots pillar 1 formalizes. Candidate areas, not final contracts.

- Runtime kernel — global coherence: turn lifecycle, message and event
  ordering, retry and fallback policy, tool-call validation, budgets,
  cancellation, durable persistence, recovery coordination, replay invariants.
- Session experience — how prior work is remembered and materialized. Flat
  transcript, tree branches, append-only event log, DAG, ephemeral,
  privacy-minimized, collaborative.
- Context construction — what the model sees: prompt derivation, transcript
  normalization, dynamic-context delivery, template policy. Now drafted as
  `context_renderer.v0.3` (provider: `context_provider.v0.2`).
- Memory — disabled, explicit notes, behavioral, project-local, semantic,
  graph, episodic, external. Extraction should be background, best-effort, and
  non-blocking.
- Tool invocation — schema publication, name and argument validation, repair
  or rejection policy, approval, sandboxing, timeouts, result shaping, artifact
  capture, capability scoping.
- Model invocation — provider normalization: streaming, tool-call formats,
  reasoning metadata, provider replay fields, rate limits, content filters,
  fallback compatibility. Contract drafted as `model_invocation.v0`, revised
  to `model_invocation.v0.1` after the runtime-kernel reality check; Go
  implementation still v0-shaped and needs the v0.1 update.
- Compression — a state transform with provenance, not a summarizer. It affects
  replay, tool-call invariants, memory provenance, prompt cache stability, and
  user trust.
- Observability and replay — the event log answers what the model saw, what it
  emitted, what was rejected, what tool ran, what changed state, why fallback
  happened, and what transforms occurred. Replay is the sharpest observability
  tool, and the model is a first-class consumer of it.
