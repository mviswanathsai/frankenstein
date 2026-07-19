# Frankenstein

Working title: Frankenstein. The name is provisional.

This project is an attempt to formalize the capability boundaries of agentic
harnesses. The goal is not to produce another monolithic agent. The goal is to
define a modular, composable harness model where major agent capabilities can be
swapped, studied, implemented, and recombined independently.

The first implementation can be simple. The important work is the taste with
which the contract boundaries are chosen and the thoroughness with which those
contracts are expressed.

## Vocabulary

Use these terms consistently.

### Capability

A capability is a replaceable harness surface with a stable contract. It is the
thing the harness knows how to depend on.

Capabilities are drawn around meaningful agent behavior, not file layout. A
capability should describe a user-visible and runtime-relevant job such as
memory, context construction, model invocation, tool invocation, compression,
session experience, recovery, observability, or UI/gateway interaction.

A capability is not a Python module, helper function, process, plugin, provider,
or microservice. Those may implement a capability, but they are not the
capability itself.

### Contract

A contract is the typed boundary for a capability.

A capability contract defines externally observable obligations, not the
internal steps used to satisfy them. It says what a service must look like from
the outside: commands accepted, events emitted, state promises, side effects,
failure semantics, replay behavior, and invariants. It does not prescribe which
internal policies, helper objects, strategies, architecture, language, or
backend a service uses.

It defines the stable cross-boundary surface:

- supported actions
- required and optional metadata
- command payload schemas
- output payload schemas
- emitted events, including terminal events
- state ownership expectations
- side effects allowed
- model-facing tools, if any
- invariants
- failure, retry, and replay semantics
- security and capability boundaries
- test fixtures

The core contract shape is a command envelope:

```text
CommandEnvelope = action + metadata + payload + causality refs
```

The output schema of an action is the payload schema of its terminal event. In a
direct-call implementation, the mediator may return that terminal event
synchronously as a convenience, but the event payload is the canonical recorded
output.

### Service

A service is a concrete implementation of one or more capability contracts.

A service may be in-process code, a local subprocess, a library adapter, a
remote API, a plugin, or a shadow/eval implementation. A service may implement
multiple whole capabilities, and multiple services may implement the same
capability at the same time.

A service implements capability contracts in wholes, not arbitrary slices. If an
implementer wants to change one internal choice inside a capability, that can be
a new service implementing the same whole capability contract. The service may
reuse code, delegate internally, or compose private strategies however it wants,
but the harness configuration should select coherent capability implementations
rather than wire every internal decision.

The important rule:

> Capability is the stable contract-shaped slot in the harness; service is the
> swappable implementation that fills that slot.

### Mediator

The mediator is the harness library/runtime layer that standardizes
cross-capability mechanics. It should own envelopes, service routing, validation,
IDs, ordering, primary/shadow mode, event append, and replay hooks.

The mediator should not own capability semantics. Services implement capability
semantics. The runtime/control flow decides when capabilities are invoked.

The first implementation should prefer direct function-like mediator calls over
an event bus:

```text
runtime -> mediator.invoke(command) -> service
service -> terminal event/output -> mediator event log
```

Events are the canonical semantic record. Direct calls are the initial execution
strategy.

### Model-Facing Tools

Some capabilities publish tools to the model. Those tools are part of the
capability contract when the model is expected to call them, but tools are not
the whole capability.

For example, a memory capability may have harness-enforced actions such as
`observe` and `recall_for_context`, while also publishing model-facing tools
such as `memory_search`, `memory_save`, `memory_forget`, or `memory_profile`.
The tool router exposes and routes those tools at runtime.

## Core Vision

Modern agent harnesses already contain a set of recurring capability areas:

- runtime loop
- model adapters
- context construction
- session and history management
- memory
- compression
- tool routing
- tool execution
- policy and permissions
- error recovery
- fallback routing
- observability
- UI and gateway surfaces

Projects like Hermes are valuable because they expose the real-world mess:
malformed tool calls, provider-specific replay metadata, huge prompts, long
session degradation, blocking memory extraction, context overflow, local model
cache invalidation, tool-result bloat, and brittle recovery paths.

The idea here is to take those learnings and apply them differently. Instead of
one large organism where every behavior is entangled, we want a framework where
an agentic harness can be composed from well-defined capabilities and services.

In the long run, a harness should be constructible from a small declarative file
that says which services fill which capabilities. The engine should pull those
services together behind the scenes and produce the requested harness.

This could be especially useful for hackathons, experiments, research, and
personal agent systems where people want to combine ideas quickly:

- use one system's memory engine
- use another system's context packer
- use a different session model
- use a strict tool router
- use a Python provider adapter
- use a Go runtime kernel
- use a Rust tokenizer or search index

The project is not anti-Python. Python remains excellent for fast-moving LLM
APIs, model adapters, memory extraction, plugins, and tool ecosystems. The
broader idea is that each service should be allowed to use the language and
runtime best suited to its job.

## Central Hypothesis

Agent harness development can be broken into meaningful substreams if the
contracts between capabilities are formalized well enough.

The useful artifact is not only a reference implementation. The useful artifact
is a capability/service model:

- what capabilities exist
- what each capability owns
- how capabilities communicate through commands and events
- what can be swapped
- what invariants must hold
- what failures must be represented
- what user-facing behavior each contract enables

If the boundaries are right, services can go wild inside those boundaries.

### Potentially Useful Side Effect: Parallel Implementations

A useful side effect of capability-level contracts is that one capability name
does not have to resolve to exactly one service. A harness can run several
services implementing the same capability at the same time, with the mediator or
runtime logistics deciding which service is primary and which services are
shadow, comparison-only, or disabled.

For example, `memory` could have one service that commits live state
while two others receive the same command envelopes and write only isolated
evaluation artifacts. The live harness still has one committed behavior, but the
event log captures comparable outputs from multiple services on the same
real turns.

This is potentially valuable for end-to-end evals: same session, same inputs,
same semantic commands, multiple services, one committed path, and replayable
comparison. It should remain a routing and logistics choice, not something every
service has to know about.

## Important Design Taste

Do not start by inventing ideal contracts.

Start from real implementations. Study what exists, where it lives, what it
does, what state it mutates, and what failures it handles. Then work upward
toward user-facing capabilities.

Important local sources:

- `/home/mviswanathsai/hermes-agent`
- `/home/mviswanathsai/pi`
- `/home/mviswanathsai/awesome-harness-engineering`
- `/home/mviswanathsai/agent-client-protocol`

The contract surface is not purely an under-the-hood implementation question.
It is also a user-facing and harness-author-facing question.

The right question is not only:

> Can this be separated technically?

The better question is:

> Would someone plausibly want to swap this set of responsibilities for a
> different philosophy?

A capability deserves a contract when it represents a meaningful replaceable
agent behavior, not merely an implementation convenience.

For example, Hermes may spread "session logic" across persistence, compression,
prompt assembly, memory, search, replay cleanup, title generation, and UI
commands. Internally those may be many pieces. From the user's perspective,
they may form one larger replaceable capability:

> how the harness remembers, resumes, branches, compacts, searches, and presents
> prior work.

That larger capability is a better contract candidate than each internal helper.

## Boundary Rule

Draw contracts around useful logical chunks.

A capability is contract-worthy when:

- swapping it changes the kind of harness being built
- multiple plausible implementations exist
- users or researchers may care about the tradeoff
- the boundary can be kept stable enough to document
- the rest of the system can treat it as a black box
- the contract captures real failure modes, not just happy paths

A capability is probably not contract-worthy when:

- it is just a helper function
- it has no meaningful alternate philosophy
- nobody would swap it independently
- it only exists because of one implementation's file layout
- separating it would create ceremony without leverage

The phrase to keep in mind:

> Contracts should be drawn around replaceable agent capabilities, not around
> implementation conveniences.

The cost of swapping a small internal policy is writing or selecting a new
service that implements the whole capability contract. That cost is intentional:
it protects capability boundaries from dissolving into arbitrary plumbing while
still allowing implementation strategies to vary freely behind the contract.

## Methodology

Work in this order. Mark the status to completed when that task is completed.

### 1. Hermes Census

Map what exists without designing the new system yet.

For each relevant Hermes module or cluster:

- file or module
- observed responsibility
- state owned or mutated
- inputs
- outputs
- external side effects
- related modules
- user-visible behavior affected
- failure cases handled
- hidden coupling

The goal is descriptive accuracy.

Status: pending

### 2. Capability Clustering

Group scattered implementation pieces into larger capabilities.

For each capability:

- user-visible job
- runtime job
- Hermes files involved
- state involved
- adjacent capabilities
- where responsibilities blur
- what the capability must preserve if replaced

One Hermes file may contribute to many capabilities. One capability may span
many Hermes files.

Status: pending

### 3. Substitution Test

Ask whether each capability deserves a contract.

Questions:

- Would someone want to swap this out?
- Would swapping it change the harness philosophy?
- Are there multiple plausible implementations?
- Is this a user-visible choice?
- Can the interface be small enough?
- Does the contract need to span multiple internal modules?

This step prevents over-contracting tiny internals.

Status: pending

### 4. Contract Draft

Only after the census and substitution test, draft the contract.

Each contract should eventually define:

- purpose
- owned state
- commands
- events, including terminal events
- inputs
- output payloads
- invariants
- failure semantics
- lifecycle
- concurrency expectations
- persistence expectations
- security and capability boundaries
- replay behavior
- test fixtures

Avoid fake elegance, and don't force elegance now. Contracts must survive real agent behavior.

Status: pending

### 5. Control Flow

After the contracts are understood, define the harness control flow:

- turn start
- context materialization
- model invocation
- stream handling
- tool-call validation
- tool execution
- recovery
- memory and compression side effects
- finalization
- persistence
- background jobs

The control flow should be event-aware, but not necessarily distributed.

Status: pending

## Semantic Events Vs Transport

This project is event-driven in the semantic sense.

That does not mean every event must immediately cross a network boundary, or
that the first implementation should use an event bus for control flow.

Prefer direct mediator invocations first. The mediator can call a service like a
function, receive the terminal event/output, and append the same event to the
log. The event is the canonical semantic record; the direct return is an
execution convenience.

An event can begin as an in-process append-only record. Later, the same semantic
event can travel over stdio, gRPC, Connect, NATS, SQLite polling, or some other
transport.

Separate the event model from the transport implementation.

Do not introduce transport overhead before the contracts justify it.

## Initial Capability Areas

These are candidate capability areas to investigate. They are not final
contracts.

### Runtime Kernel

The runtime kernel owns global coherence:

- turn lifecycle
- message and event ordering
- retry and fallback policy
- tool-call validation before execution
- budget enforcement
- cancellation and timeouts
- durable persistence
- recovery coordination
- replay invariants

This is a strong candidate for a small, strict core.

### Session Experience

This is not just storage. It includes how prior work is remembered and
materialized.

Possible implementations:

- flat transcript
- tree sessions with branches
- append-only event log
- DAG with shared prefixes
- ephemeral eval sessions
- privacy-minimized sessions
- collaborative sessions

Important distinction:

Session storage and context materialization are related but not the same thing.

### Context Construction

Context construction decides what the model sees.

It may include:

- system prompt assembly
- stable/context/volatile prompt partitioning
- project instructions
- memory injection
- tool schema inclusion
- token budgeting
- prompt caching strategy
- transcript compression
- branch-aware materialization

This is one of the most important research surfaces.

### Memory

Memory may be:

- disabled
- explicit user notes
- automatic behavioral memory
- project-local memory
- semantic memory
- graph memory
- episodic memory
- external memory provider

Memory extraction should often be background, best-effort, and non-blocking.

### Tool Invocation

Tool invocation includes:

- tool schema publication
- tool name validation
- argument validation
- repair or rejection policy
- approval policy
- sandboxing
- timeout handling
- result shaping
- artifact capture
- capability scoping

This is a major contract candidate because different harnesses may want very
different safety and autonomy tradeoffs.

### Model Adapter

Model adapters normalize provider-specific APIs.

They must handle:

- streaming
- tool-call formats
- reasoning metadata
- provider-specific replay fields
- rate limits
- content filters
- malformed provider responses
- fallback compatibility

The core should preserve provider-specific metadata without letting it infect
unrelated capabilities.

### Compression

Compression is not just "summarize old chat."

It affects:

- replay correctness
- tool-call invariants
- memory provenance
- prompt cache stability
- user trust
- long-session usability

Compression should have provenance and should be treated as a state transform,
not an invisible mutation.

### Observability And Replay

Agent systems need replay because failures are often emergent.

The event log should make it possible to answer:

- what did the model see?
- what did it emit?
- what was rejected?
- what tool ran?
- what changed state?
- why did fallback happen?
- what compression or memory transform occurred?

Observability should support debugging, evals, and research.

## Language Philosophy

Languages should play to their strengths.

Possible allocation:

- Go: runtime kernel, gateway, scheduler, persistence, cancellation, budgets
- Python: provider adapters, LLM-heavy transforms, memory extraction, rich tools
- Rust: tokenization, search, indexing, patch parsing, sandbox-sensitive helpers
- TypeScript: UI, browser automation, interactive inspectors

This is not a rule. It is a starting point.

The important point is that the capability contract should be independent of the
implementation language.

## Non-Goals

Do not make this a Hermes rewrite.

Do not start with microservices.

Do not split every helper into a capability or service.

Do not make the harness configuration a wiring diagram of every internal step
inside a capability.

Do not make event transport the first problem.

Do not assume the first implementation proves the contracts are complete.

Do not optimize for mass adoption at the expense of research clarity.

Do not remove Python from the ecosystem for ideological reasons.

Do not confuse a clean implementation with a correct boundary.

## Reference Implementations To Study

Hermes is the primary case study because it contains many real-world agent
runtime lessons.

Local source directories:

- `/home/mviswanathsai/hermes-agent`
- `/home/mviswanathsai/pi`
- `/home/mviswanathsai/awesome-harness-engineering`
- `/home/mviswanathsai/agent-client-protocol`

Other useful references may include:

- Pi, for minimalism, self-extensibility, and branchable sessions
- Claude Code style project instruction files
- Codex-style repo guidance through AGENTS.md
- OpenAI/Anthropic/Codex provider behavior
- local model harnesses and prompt-cache behavior
- MCP-style tool boundaries

The point is not to copy any one harness. The point is to extract capability
boundaries from real systems.

## Immediate Next Steps

Start with a Hermes implementation census.

Suggested first pass:

1. Runtime loop and turn lifecycle
2. System prompt and context construction
3. Tool registry and tool execution
4. Provider normalization and model adapters
5. Session persistence and replay
6. Compression and long-session handling
7. Memory extraction and memory injection
8. Recovery, fallback, and error classification
9. UI/gateway interaction
10. Observability, traces, and eval surfaces

For each, produce a dossier with this shape:

```text
Capability:
User-visible job:
Runtime job:
Hermes files involved:
State owned or mutated:
Inputs:
Outputs:
External effects:
Failure modes:
Recovery behavior:
Hidden coupling:
Possible alternate philosophies:
Contract-worthy? yes/no/maybe:
Reason:
```

Only after these dossiers exist should the project draft formal contracts.

## Working Principle For Future Agents

When working on this project, do not rush into implementation.

Prefer:

1. observe concrete harness behavior
2. identify capability clusters
3. test whether users would care about swapping the capability
4. define the smallest useful contract
5. then implement a reference version

Implementation is allowed, but the project lives or dies by contract taste.
