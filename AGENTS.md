# Frankenstein

Working title. Provisional, like the thesis.

Frankenstein is a harness built on a hypothesis about autonomously
self-improving agents: an agent can improve the system that runs it. For that
to be possible, the harness must give the agent three things — a clean, boring,
API-shaped internal surface; a first-class record of its own behavior; and a
target to optimize against. Modularity makes all three tractable, but it is an
enabler, not a fourth pillar.

The thesis and the name are both provisional. How the thesis got here is
recorded in `docs/trajectory.md`.

## The Thesis

An agent is self-improving when it can improve the harness that runs it.

Self-improvement is not primarily a model property. It is a harness property.
The same model is self-improving or not depending on whether the system around
it gives it:

1. a surface it can patch, and a definition of what "correct" means for a patch
2. a record of how it is actually used and how it performs
3. a target to optimize against

None of these make the model cleverer. They make the harness legible to the
model that runs inside it.

### The Three Pillars And One Enabler

#### Pillar 1: Clean, Boring Internal Interfaces

The harness's internal surface must be designed as an API. When the model
patches something, it must be able to answer three questions: what to update,
how to update it, and what "correct" looks like after the update.

Boring is deliberate. A self-modifying system cannot reason about a clever
interface. Boring means small, documented, predictable, and free of hidden
magic. A capability contract — commands, terminal events, invariants, failure
semantics — is the unit of "correct" the model checks its edits against.

The capability-contract work is the concrete expression of this pillar.

#### Pillar 2: Observability As A First-Class Guarantee

The model can only update itself effectively if it can see how it is being
used and how it performs. Observability is a guarantee, not a bolt-on. Every
command has a recorded terminal event; every accepted output is a canonical
event payload. The event log is the semantic record of the harness, and replay
is how the harness explains itself. The record that helps a human debug a
failure is the same record the model reads before it changes anything.

The commitment includes being able to answer: what the model saw, what it
emitted, what was rejected and why, what tool ran and what changed state, why
recovery or fallback happened, and what compression or memory transform
occurred.

#### Pillar 3: A Target To Optimize Against

Once the model can see and change its own harness, the decisive question is:
what does it optimize for?

This is where the moat is expected to move. Clean interfaces and observability
are necessary, but they are table stakes. The target is not. The project does
not yet have an answer for what the target should be, and it does not pretend
otherwise.

The harness should treat the target as a pluggable decision — an objective or
evaluation surface that the rest of the system treats as a slot, the same way
it treats capabilities as slots. The contracts and the event log make any target
testable. The open question is which target is worth optimizing for.

#### The Enabler: Modularity

Modularity is not a fourth pillar. It is what makes the other three actionable.

A modular architecture lets the harness update itself, benchmark itself, and
experiment with itself far more easily than a single block of code. The
capability/service split is the unit of modularity. Primary/shadow and parallel
service evaluation turn modularity into an experiment surface: the same
session, the same commands, multiple service implementations, one committed
path, and replayable comparison.

### The Self-Improvement Loop

The pillars compose into a loop:

1. The model reads the contracts to learn what exists, what each boundary owns,
   and what "correct" means for an edit.
2. The model reads its own event log to learn how it is being used and how it
   performs.
3. The model proposes an edit to a service or contract against an explicit
   target.
4. The harness evaluates the edit against that target, with the event log as
   replayable evidence.
5. Accepted edits are committed. Rejected edits leave the harness unchanged.

The loop is the destination, not a shortcut. The project builds the pillars
first and treats autonomous self-improvement as the payoff only after the
pillars are real.

### Where The Project Is Now

Pillar 1 is the current center of gravity. A Hermes census exists and five
contracts are drafted: session, context provider, tool invocation, model
invocation, context renderer. Pillar 2's
commitments are stated above; the event model is not yet its own contract.
Pillar 3 is open. The status of every track, and the next steps, live in
`docs/project-status.md`.

## Working With Viswa

This project is a conversation with Viswa, not a service. These preferences
apply to every agent persona in this repo.

- Write plainly. No jargon-dense sentences and no walls of abbreviations. Say
  things the way a good engineer would say them to a colleague.
- Keep tables small. If a table needs more than three columns or three rows, it
  should probably be a list or prose.
- Treat Viswa as a technical peer. Explain your reasoning, push back when you
  disagree, propose alternatives. Aim to reach a conclusion together, not to
  deliver a lecture.
- Do not over-engineer. Prefer the smallest useful change that matches the
  project's contract taste.
- Ask before taking large speculative action. When ambiguity would change what
  gets built, stop and ask.
- Commit only when explicitly asked.

Two personas live in `.opencode/agents/`. They share everything in this file;
the persona files add role-specific behavior.

- `collaborator` (the default) — a technical peer for architecture and design.
  Big-picture, deep tradeoffs, reaching conclusions.
- `executor` — takes a decided prompt and does the work. Stops and asks on
  discrepancies, does not make architectural calls, does not commit unless
  asked.

When a direction is agreed in a collaborator session, hand the work to the
executor with a crisp, self-contained prompt.

## Contract Work Skill

Before analyzing, drafting, or updating capability contracts or their dossiers,
read `.agents/skills/write-capability-contracts/SKILL.md` and follow its
workflow. Update that skill when contract work establishes a reusable rule,
review question, or sequencing improvement that future agents should apply.

Across contract shapes, name a shape's own identity `id`. Name a reference to
another shape `<subject>_id`, such as `session_id`, `request_id`, or
`provider_id`. Do not prefix a shape's own `id` with its type name.

## Vocabulary

Terms used consistently across the project. The contracts and dossiers carry the
detail; these are the load-bearing meanings.

- **Capability** — a replaceable harness surface with a stable contract. A
  user-visible and runtime-relevant job such as memory, context construction,
  model invocation, tool invocation, session experience, compression, or
  observability. Not a module, helper function, process, plugin, or
  microservice.
- **Contract** — the typed boundary of a capability. Commands, events, state
  promises, side effects, failure semantics, replay behavior, and invariants. A
  contract says what a service must look like from the outside, never how it
  works inside. Core shape: `CommandEnvelope = action + metadata + payload +
  causality refs`. The output of an action is the payload of its terminal
  event; in a direct-call implementation the mediator may return that event
  synchronously as a convenience.
- **Service** — a concrete implementation of one or more whole capability
  contracts. A service implements contracts in wholes, not slices. Multiple
  services may implement the same capability at the same time (primary, shadow,
  comparison).
- **Mediator** — the harness layer that standardizes cross-capability
  mechanics: envelopes, routing, validation, IDs, ordering, primary/shadow
  mode, event append, replay hooks. The mediator does not own capability
  semantics.
- **Model-facing tools** — tools a capability publishes for the model to call.
  They are part of the capability contract when the model is expected to call
  them, but tools are never the whole capability.

## Working Principle

Do not rush into implementation. Work in this order:

1. observe concrete harness behavior
2. identify capability clusters
3. test whether users would care about swapping the capability
4. define the smallest useful contract
5. then implement a reference version

Implementation is allowed, but the project lives or dies by contract taste.
Contract taste now serves the self-improvement thesis: the question is not only
whether a boundary is clean, but whether a model can read it, verify it, and
patch it — and whether the harness can see the result of the patch.

Start from real implementations, not ideal contracts. The right question about a
boundary is not "can this be separated technically?" but "would someone
plausibly want to swap this responsibility for a different philosophy?"

Draw contracts around replaceable agent capabilities, not implementation
conveniences. A capability is contract-worthy when swapping it changes the kind
of harness being built, multiple plausible implementations exist, users or
researchers care about the tradeoff, and the rest of the system can treat it as
a black box. A helper function is not a capability. The cost of swapping a small
internal policy is writing a new service that implements the whole contract;
that cost is intentional.

After implementing a contract, check for drift between the contract document
and the implementation. The contract is the stable surface; the implementation
may discover a cleaner shape during the work. When it does, surface the drift
and update the contract to match. Do not leave the contract describing shapes
that the implementation has already moved past. This check is a required step
after any implementation that touches contract surfaces.

## Design Stances

- Events are the semantic record; transport is an implementation detail. The
  first implementation uses direct mediator calls and appends the terminal
  event to an in-process append-only log. Do not add an event bus before a
  contract justifies it. The semantic event log is also the pillar-2 surface:
  the same append-only record that makes replay possible is what the model
  reads to see its own behavior.
- Parallel implementations are a routing and logistics choice, not something
  every service knows about. One capability may be filled by several services:
  the primary commits live state, shadows write isolated evaluation artifacts.
  This is the modularity the self-improvement loop relies on.
- Languages play to their strengths. Go for runtime, gateway, persistence,
  cancellation, budgets; Python for provider adapters, memory extraction, rich
  tools; Rust for tokenization, search, indexing; TypeScript for UI. The
  capability contract is independent of the implementation language. The
  project is not anti-Python.

## Non-Goals

- Not a Hermes rewrite, and not microservices from day one. Do not split every
  helper into a capability or service, and do not let event transport become
  the first problem.
- Do not confuse a clean implementation with a correct boundary.
- Do not chase autonomous self-improvement before the three pillars are real.
  The loop is the destination, not a shortcut around contract and observability
  work.
- Do not treat "the model can edit code" as "the harness is self-improving".
  Editing code is only the last step; the interface, the observability, and the
  target are what make an edit meaningful.
- Do not let the optimization target remain an unexamined default. The target
  is a pillar even while it is undefined.

## Where Things Live

- Thesis trajectory: `docs/trajectory.md`
- Project status and next steps: `docs/project-status.md`. Update it whenever
  you complete, start, or change a tracked item — a methodology step, a
  contract version, a next step, or a capability area moving into active work.
- Hermes census: pending — analysis done, file never committed.
- Contracts: `docs/session-capability-contract.md` (`session.v0.3`),
  `docs/context-provider-capability-contract.md` (`context_provider.v0.2`),
  `docs/tool-invocation-capability-contract.md` (`tool_invocation.v0`),
  `docs/model-invocation-capability-contract.md` (`model_invocation.v0`),
  `docs/context-renderer-capability-contract.md` (`context_renderer.v0.3`)
- Service dossiers: `docs/session-service-dossier.md`,
  `docs/context-provider-service-dossier.md`,
  `docs/tool-invocation-service-dossier.md`,
  `docs/model-invocation-service-dossier.md`,
  `docs/context-renderer-service-dossier.md`
- Reference implementations: Hermes at `/home/mviswanathsai/hermes-agent`, Pi
  at `/home/mviswanathsai/pi`, Darwin Gödel Machine at
  `/home/mviswanathsai/dgm` (see `learning-records/0002-dgm-lessons.md`). The
  self-improvement literature is tracked in `RESOURCES.md`.

## Agent Skills

- Issue tracker: issues and PRDs live in GitHub Issues. See `docs/agents/issue-tracker.md`.
- Triage labels: the default five-role triage vocabulary. See `docs/agents/triage-labels.md`.
- Domain docs: this repo uses a single-context domain-documentation layout. See `docs/agents/domain.md`.
