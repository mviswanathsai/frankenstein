# Tool Exposure Catalog Plan

Date: 2026-08-03

Status: non-normative implementation plan.

This plan defines the smallest primitives needed to record the canonical tool
definitions a model has been shown over a session. It does not change
`tool_invocation.v0` or the runtime's callable tool catalog.

## Two Catalogs

`ToolCatalog` remains the exact ordered definitions supplied through a model
provider's callable-tool mechanism. The Runtime Kernel may hold its active ID,
and Model Invocation encodes it for the provider.

`ToolExposureCatalog` is cumulative semantic evidence: the latest canonical
definition of each tool that has been fully disclosed to the model so far. It
is never supplied to Model Invocation and never participates in dispatch,
authorization, validation, or catalog adoption.

The two catalogs initially contain the same definitions. A successful
`tool_describe` can later advance only the exposure catalog.

## Logical Shape

```text
ToolExposureCatalog {
  id
  tools: ToolDefinition[]
}
```

`ToolDefinition` is reused from `tool_invocation.v0`; no exposure-local copy is
introduced.

The ID is content-derived from the artifact kind, format version, and exact
ordered canonical definitions. Repeated identical exposure states therefore
share one ID across sessions and restarts. Its namespace must be distinct from
`ToolCatalog.id` even when both artifacts contain the same definitions.

The logical artifact contains complete definitions. A persistence adapter may
deduplicate definition bodies and store the catalog as a manifest of definition
revisions; that storage layout is not part of the contract.

## Ownership And Seam

The exposure projector is a deep observability module with one interface:

```text
project(previous_exposure_catalog_id?, ordered_evidence) -> exposure_catalog_id
```

Behind that interface it resolves immutable artifacts, applies disclosure
rules, canonicalizes ordering, deduplicates definitions, persists the resulting
snapshot, and returns its content-derived identity.

Responsibilities are split as follows:

- Tool Invocation owns canonical `ToolDefinition` values and emits structured
  evidence identifying the definition returned by `tool_describe`.
- Model Invocation evidence establishes which tool results were actually
  included in a provider request. Producing a description result does not prove
  that the model saw it.
- The exposure projector folds those ordered facts into immutable snapshots.
- The artifact store persists and deduplicates snapshot bodies.
- An observability index may later map a session event position or model
  invocation to an exposure-catalog ID.

The projector is rebuildable from canonical events. Projection or index failure
must not change live tool execution; recovery replays the evidence.

## Projection Rules

For the first model invocation, seed the exposure catalog from the callable
`ToolCatalog` used for that invocation.

For later model invocations:

1. Start from the preceding exposure snapshot in the same linear session.
2. Find successful `tool_describe` results included in this provider request.
3. Resolve their structured canonical `ToolDefinition` evidence.
4. Add a newly disclosed tool at the end.
5. When the same stable tool ID is disclosed at a new revision, replace its
   prior definition in place.
6. Canonicalize and persist the complete snapshot.
7. Reuse the existing ID when no fully disclosed definition changed.

Historical snapshots retain earlier revisions. A definition changing in the
registry does not change exposure state until that revision is actually shown
to the model.

`tool_search` does not add entries because summaries are not complete
`ToolDefinition` values. Search results remain ordinary exact model-input
evidence. Proxy calls record use through call events; they do not by themselves
prove that the model saw the target definition.

## Required Primitives

Implement these in order:

1. A domain-separated canonical serializer and digest for
   `ToolExposureCatalog`.
2. Structured description evidence correlated with the describe call and its
   canonical tool definition revision.
3. Model Invocation evidence that identifies which normalized tool results
   were included in the provider request, or an equivalent exact input artifact
   from which that fact can be recovered.
4. A pure exposure projection function.
5. Immutable artifact persistence and lookup by exposure-catalog ID.
6. A rebuildable index from model-invocation/event position to exposure-catalog
   ID.

Event payload placement for the exposure-catalog ID is intentionally deferred.
The primitives above must not assume whether the final reference lives on a
Model Invocation event, a separate projection event, or an observability index.

## Verification

The first implementation should prove:

- the initial exposure snapshot contains the callable catalog definitions
- repeated identical input produces the same ID
- a produced-but-never-delivered description does not change exposure
- a delivered successful description appends one definition
- repeating the same description keeps the same ID
- a newly delivered definition revision replaces the earlier revision in place
- failed or unavailable descriptions do not change exposure
- search summaries and proxy use do not add full definitions
- exposure projection never changes the callable catalog
- replaying the same evidence reconstructs the same snapshot IDs
- two sessions with identical exposure state share stored content

## Non-Goals

- changing the Runtime Kernel's active callable catalog
- granting execution authority from prior exposure
- replacing exact provider-request or transcript evidence
- recording every partial tool summary as a full definition
- defining branch and merge semantics before branching exists
- choosing the final event that references `ToolExposureCatalog.id`
- prescribing a database or blob-store layout

## Open Questions

- Which Model Invocation evidence most compactly proves that a particular tool
  result reached the model?
- Should exposure-catalog references be attached to each Model Invocation event
  or emitted as separate derived projection events?
- What retention and garbage-collection rule applies when retained events refer
  to exposure snapshots?
- When branching is introduced, does each branch inherit the parent's exposure
  head and then advance independently?
