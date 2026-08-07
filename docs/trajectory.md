# Frankenstein Thesis Trajectory

Date: 2026-08-06

Status: a history, not a normative statement.

This document records how the project's thesis arrived where it is. For the
current direction, see `AGENTS.md`. The `learning-records/` directory captures
individual working sessions; this document records the thesis-level shifts.

## Where the project is now

Autonomously self-improving agents. The funnel ends at a harness that lets an
agent improve the harness that runs it, built on three pillars plus an enabler:

1. clean, boring internal interfaces designed as APIs
2. observability as a first-class guarantee
3. a target to optimize against
4. modularity (the enabler, not a pillar)

None of the four arrived first or fully formed. Each step below added a piece.

## The journey

### 1. Multi-language modular services

Different languages are stronger at different things an agent must do. Program
the agent as a set of modular services that can communicate with each other
instead of one solid block of Python.

Origin: the author was not a fan of one big Python block. The insight carried
forward is that a service should be allowed to use the language and runtime best
suited to its job. It also seeded the modularity that later became the enabler.

### 2. Mapping harness slices into defined APIs

No one had mapped the "slices" of an agentic harness into defined APIs. Seeing
value in doing this correctly — it could give harness engineering a more
structured perspective as its own engineering stream.

This is where the capability-contract work was born: the Hermes census, the
service dossiers, the substitution test, and the first drafted contracts
(session, context provider, tool invocation). In this stage the contracts were
close to the end goal.

### 3. Observability becomes first-class

While coding, good observability cascaded into many fruitful things. The design
decision was made to keep observability a first-class citizen rather than a
bolt-on: every command gets a recorded terminal event, every accepted output is
a canonical event payload, and replay is the way the harness explains itself.

This later became pillar 2, and the surface the model reads before it edits
itself.

### 4. The self-updating agent

Once a harness existed on those principles, having the agent update itself felt
almost trivial — and served as a testament that the design works. Given the
principles already settled (API surface, observability), self-editing looked
like a natural payoff rather than a separate invention.

This is how pillars 1 and 2 started to look like means rather than ends.

### 5. Autonomously self-improving agents

The current thesis. Retains the service/multi-language instinct, the contract
work, and the observability commitment, but reframes their shared purpose: a
harness the model inside it can improve. The optimization target joins the
surface and the observability as one of three required guarantees.

## What changed in the framing

| From (earlier stages) | To (current) |
|---|---|
| Capability contracts as the end goal | Pillar 1: the API-shaped surface the model reads before editing |
| Multi-language modularity as the thesis | The enabler — what makes the other three tractable |
| Observability as one capability area | Pillar 2 — a first-class guarantee of how the model is used and performs |
| The objective as unexamined background | Pillar 3 — the target slot, where the moat moves |
| Harness engineering as a descriptive stream | A harness with the pillars, whose destination is self-improvement |

## What to keep from each stage

- from 1: services may use the language and runtime best suited to their job
- from 2: the vocabulary, the boundary rule, the methodology, and the drafted
  contracts
- from 3: the semantic event model, replay, exposure catalogs, and the
  "what did the model see" questions
- from 4: the self-updating loop as the payoff that the pillars are in service of
- from 5: the target as the slot the project's moat will occupy

## Related records

- `AGENTS.md` — current direction and pillars.
- `learning-records/0001-harness-foundations.md` — early model/harness/agent
  framing.
- `learning-records/0002-dgm-lessons.md` — the Darwin Gödel Machine as the
  minimal self-editing harness the pillars are meant to fix.
- `RESOURCES.md` — the self-improving-harness literature.