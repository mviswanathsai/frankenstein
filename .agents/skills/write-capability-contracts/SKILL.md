---
name: write-capability-contracts
description: Analyze, draft, review, or update Frankenstein capability contracts and service dossiers. Use for any work that changes contract shapes, commands, events, shared types, ownership boundaries, invariants, failure semantics, or dossier-derived capability requirements.
---

# Write Capability Contracts

## Workflow

1. Read `AGENTS.md`, the new or affected dossier, and every existing contract
   that owns or already expresses related data or behavior.
2. Treat the dossier as rough scratch space. Extract possible needs from it; do
   not treat its proposed names or shapes as authoritative.
3. Build a reuse table before drafting: dossier need, existing owner, existing
   type or field, semantic match, and actual gap.
4. Reuse an existing concrete type when its semantics reasonably match. Do not
   create a capability-local synonym, projection, or renamed field merely for
   convenience.
5. When a real need requires an existing shared type to change, update that type
   in its owning contract first. Then reference the updated type from the new
   contract.
6. Draft only the genuinely new capability surface after reconciliation.
7. Compare the result back against all affected contracts for names, required
   fields, ownership, lifecycle, failure, retry, replay, and security semantics.
8. Record durable discussion outcomes that are not yet normative in the
   affected dossier. Label accepted directions, tentative implementation
   choices, and open questions distinctly; keep them out of the contract until
   they create an externally observable obligation.
9. Update this skill when the work reveals a reusable workflow rule or review
   question.

Do not treat an existing contract outline as a required template. Include only
headings the user has explicitly requested or the current contract discussion
has deliberately established. Do not copy provisional sections such as service
advertisement, concurrency, persistence, security, or replay into another
contract merely for structural completeness.

## Shape Discipline

Treat shapes as externally observable command inputs and event outputs, not as
persistence schemas. Owned state describes semantic responsibility; it does not
prescribe tables, documents, indexes, or internal fields. A field belongs in a
contract only when an action, event, invariant, replay requirement, or external
consumer needs to observe it. "A service might store this" is not sufficient.

For every described shape:

- identify its owning contract
- mark required and optional fields explicitly
- explain why every field crosses the capability boundary
- identify who produces and consumes each field
- name the action input or event output through which the field is observed
- define field semantics, not only example values
- reuse shared identifiers, references, records, and metadata types by name
- remove fields justified only by possible future implementations
- allow an extension only when its semantics and preservation rules are clear

For a `v0` contract, require a concrete need today. Do not add a field merely
because a future implementation, policy, transport, or consumer might use it.
Add the field in a later version when that need becomes observable.

Do not introduce vague fields such as `raw`, `origin`, `metadata`, or `state`
without defining what they mean, why the receiver needs them, and which
capability owns their semantics. Do not encode provider-native formatting in a
provider-neutral session type.

Do not add a generic provenance shape when existing shared references identify
source material and parent-level identity identifies the producer. Add only the
specific source, transformation, or freshness fact that a current consumer
needs; do not preserve a provenance container merely as a future extension
point.

Prefer one canonical content field. Do not split normalized, raw, rendered, or
provider-formatted variants across fields until an observed consumer needs both
representations and the contract defines precedence between them.

Distinguish usable content from source identity. Require non-empty content on
candidate shapes; let optional candidate refs identify their sources rather
than substitute for content. Do not add a separate reference-handoff output
until a concrete consumer needs it. Define whether input refs may be silently
dropped and how the producer reports refs it did not dereference. Do not turn a
single unread ref into a terminal operation failure unless the capability
cannot otherwise return a useful result.

Name a shape's own identity `id`. Name a reference to another shape
`<subject>_id`, such as `session_id` or `request_id`. Do not redundantly prefix
a shape's own `id` with its type name.

Shape collections for the primary consumer's access pattern. When consumers
retrieve candidates by a mutually exclusive category, prefer category-keyed
buckets over repeating the category discriminator on every candidate. Keep
candidate ordering inside each bucket when order remains meaningful.

Do not repeat immutable parent metadata on every contained child. Keep it on
the parent unless children are independently transported, persisted, or
interpreted outside that parent boundary.

Do not expose scores such as confidence, relevance, or quality without a
defined type, scale, calibration domain, and consumer decision. Prefer ordered
output when the producer only needs to communicate relative ranking.

Do not infer control flow from shared data shapes. Reusing a type does not mean
the capability that owns the type must be invoked first or that one service
depends on another service. When reuse is only a practical early-version
shortcut, say so explicitly, name who else may produce the value, and leave room
to generalize the boundary when observed needs diverge.

Distinguish semantic ownership of an artifact from orchestration custody. A
runtime may request, hold, select, and route an immutable artifact without
owning its construction rules or being allowed to modify it. Name the sole
producer, the component that chooses when to use or refresh it, and any adapter
that may encode it without changing its canonical meaning.

When a model-facing action also changes later runtime inputs, keep its ordinary
model-facing result separate from a typed runtime control output. Put the
control output on the command's terminal payload, identify the immutable base
when replacement is conditional, and require the runtime to apply it before the
dependent capability call. Do not hide the change in result text, generic
metadata, an arbitrary flag scanner, or a plugin hook.

Treat staleness as relative to an owning lineage or active head, not as an
intrinsic property of an immutable artifact. If branching is out of scope,
state that constraint instead of adding speculative branch identity; a later
branching contract can define independent heads and conflict rules.

Separate publication eligibility from transient runtime availability. Stable
registration, enablement, authorization, policy, and definition validity may
control whether something is published; a momentary liveness observation does
not guarantee later execution and should not churn an immutable published
artifact. When a provider-facing type must express both, use distinct facts
rather than overloading one `available` field.

Keep location, discovery scope, and access authority distinct. A working
directory may anchor relative resolution without granting access. When a
request carries an access boundary, define empty and omitted behavior, require
the receiver to enforce the current boundary, and do not let caches or earlier
requests preserve revoked authority.

Separate resource containment from caller authorization. A sandbox may enforce
which files, processes, networks, credentials, or capability handles an
execution can reach; RBAC or another authorization policy decides which
authenticated principal may request an action. The point that receives the
principal and requested action is a natural enforcement point, but that does
not make it the owner of identities, roles, memberships, or policy. Keep
runtime-supplied caller identity out of model-written arguments.

For model-facing tools, keep model-written arguments separate from
runtime-supplied execution context and authority. A model-facing schema must not
let the model choose its session identity, caller identity, workspace grants,
approval state, or policy. Name each runtime-supplied fact explicitly rather
than passing a generic agent-state or metadata object.

For progressive tool discovery or proxy tools, distinguish the model-visible
bridge from the effective tool target. Define how the target is scoped,
resolved, validated, authorized, approved, executed, and recorded. A search,
describe, or proxy call must not bypass the current registration, validation,
authority, or policy rules that would apply if the effective tool were directly
visible. Do not assume that returning a schema as ordinary tool-result text
makes the target provider-natively callable. Direct progressive disclosure must
also publish the definition through the provider's callable-tool mechanism; if
that mechanism is unavailable, the alternatives are a later expanded tool
bundle, an explicit proxy/custom call protocol, or no progressive direct call.

Distinguish provider-native callable exposure from definitions disclosed through
ordinary model-visible results. Keep the callable catalog exact to the
provider's callable-tool input. If observability needs a cumulative exposure
snapshot, model it as a separate immutable artifact and advance it only when
Model Invocation evidence proves that the disclosure reached the model; a Tool
Invocation result being produced is not enough.

When an operation can have side effects, represent call outcome separately from
side-effect certainty. Timeout, cancellation, disconnection, or an error return
does not prove that no effect happened. Contracts must preserve `unknown` or
partial effect outcomes so retry and recovery do not silently duplicate work.

Separate a producer's semantic lifetime recommendation from a consumer's
retention policy. A producer may distinguish ongoing context from one-call
context when the consumer cannot infer that distinction, while the consumer
still owns acceptance, caching, indexing, replacement, and eviction. State
explicitly whether repeated responses are complete snapshots or deltas.

## Reconciliation Questions

Ask, in order:

1. Does another contract already own this information?
2. Is the existing type semantically sufficient, even if the dossier used a
   different name?
3. Would a projection lose identity, ordering, version, provenance, or replay
   information?
4. Is a new field required by observed behavior or only imagined flexibility?
5. Does adding the field imply a lifecycle operation the contract must allow?
6. Can derived or implementation-private data stay outside the contract?

Prefer one focused clarification at a time when user intent is ambiguous.
Listen for repeated assumptions in the user's wording and surface the most
consequential unstated assumption without derailing the current decision.

## Contract Review Output

When comparing contracts, report:

- exact agreements
- semantic agreements with different names
- unjustified duplicate shapes
- missing shared definitions
- intentional capability-local evidence
- the smallest proposed reconciliation

Resolve discrepancies sequentially when requested. Do not advance to the next
one until the user accepts the current resolution.
