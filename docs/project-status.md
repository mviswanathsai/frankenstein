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

1. Hermes census — first pass complete. See
   `docs/hermes-architecture-census.md`. Targeted follow-ups remain and are
   listed there.
2. Capability clustering — partial. Reasoning lives in the census and the
   drafted dossiers.
3. Substitution test — partial. The dossiers record contract-worthy judgments.
   The remaining capability areas need the same treatment.
4. Contract draft — partial. Five contracts drafted, listed below.
5. Control flow — pending. A development scaffold exists in the census; the
   formal control flow depends on the runtime-kernel contract.

## Drafted Contracts

- session — `session.v0.3` — `docs/session-capability-contract.md`
- context provider — `context_provider.v0.1` —
  `docs/context-provider-capability-contract.md`
- tool invocation — `tool_invocation.v0` —
  `docs/tool-invocation-capability-contract.md`
- model invocation — `model_invocation.v0` —
  `docs/model-invocation-capability-contract.md` (dossier at
  `docs/model-invocation-service-dossier.md`)
- context builder — `context_builder.v0` —
  `docs/context-builder-capability-contract.md` (implemented in
  `internal/contextbuilder/`; dossier at
  `docs/context-builder-service-dossier.md`)

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
3. Context builder (`context_builder.v0`) is implemented in
   `internal/contextbuilder/` — `estimate`, `assemble`, and `prepare` with
   test coverage. Wiring it into the harness now depends on the runtime-kernel
   contract. Sizing inputs remain for the next pass: `tool_invocation.v0`,
   `context_provider.v0.1`, and `session.v0.3` do not yet accept token budgets
   on `list_tools`, `get_context`, and `get`.
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
- Context construction — what the model sees: prompt assembly, stable and
  per-call partitioning, project instructions, memory injection, tool schemas,
  token budgets, prompt caching, transcript compression. Parts are now drafted:
  `context_provider.v0.1` and `context_builder.v0`.
- Memory — disabled, explicit notes, behavioral, project-local, semantic,
  graph, episodic, external. Extraction should be background, best-effort, and
  non-blocking.
- Tool invocation — schema publication, name and argument validation, repair
  or rejection policy, approval, sandboxing, timeouts, result shaping, artifact
  capture, capability scoping.
- Model invocation — provider normalization: streaming, tool-call formats,
  reasoning metadata, provider replay fields, rate limits, content filters,
  fallback compatibility. Now in active contract work as `model_invocation.v0`
  with its service dossier.
- Compression — a state transform with provenance, not a summarizer. It affects
  replay, tool-call invariants, memory provenance, prompt cache stability, and
  user trust.
- Observability and replay — the event log answers what the model saw, what it
  emitted, what was rejected, what tool ran, what changed state, why fallback
  happened, and what transforms occurred. Replay is the sharpest observability
  tool, and the model is a first-class consumer of it.
