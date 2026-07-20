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
8. Update this skill when the work reveals a reusable workflow rule or review
   question.

## Shape Discipline

For every described shape:

- identify its owning contract
- mark required and optional fields explicitly
- explain why every field crosses the capability boundary
- identify who produces and consumes each field
- define field semantics, not only example values
- reuse shared identifiers, references, records, and metadata types by name
- remove fields justified only by possible future implementations
- allow an extension only when its semantics and preservation rules are clear

Do not introduce vague fields such as `raw`, `origin`, `metadata`, or `state`
without defining what they mean, why the receiver needs them, and which
capability owns their semantics. Do not encode provider-native formatting in a
provider-neutral session type.

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
