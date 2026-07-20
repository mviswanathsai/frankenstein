# Context Provider Service Contract Draft

Date: 2026-07-18

Contract version: `context_provider.v0`.

Status: draft.

Contract draft: `docs/context-provider-capability-contract.md`.

This document replaces the earlier single-action context-provider dossier with a
contract-shaped draft. It is still not a final API specification. The goal is to
capture the boundary decisions from the Hermes census and the design discussion
before implementation hardens them accidentally.

The important naming decision remains: `context provider` is the umbrella.
Memory backends, project-context loaders, identity/profile systems, repo-aware
retrievers, Obsidian/vault search, skill loaders, and Hermes-style mixed context
loaders can all be context providers. `memory` is narrower: a stateful provider
that owns durable recall or update behavior.

Context providers should be coarse, swappable context strategies. They should
not become a swarm of tiny providers such as `DateProvider`, `CwdProvider`,
`EvidenceProvider`, or `PersonalityLineProvider`. Runtime facts that the session
or runtime already knows can travel in the context request. Evidence, file refs,
identity, user facts, skills, and project instructions are candidate roles or
slots, not mandatory service boundaries.

Note: an even simpler, future abstraction could be a "ranking" middleware for
context candidates that decides what is stable vs per-turn.

## Capability

Capability:
Context provider.

User-visible job:
Supply the agent with relevant background, instructions, facts, references,
skills, and retrieved material so the model can answer or act with the right
context without the harness hardcoding one memory, project-context, or retrieval
philosophy.

Runtime job:
Given a context request, return structured context candidates that another
capability can assemble into model input. A provider may discover, retrieve,
rank, label, pre-render, or reference context according to its own philosophy,
but it does not build the final prompt/transcript shape.

Contract-worthy? yes.

Reason:
Swapping a context provider changes what the agent knows, how context is
retrieved, what sources are trusted, what becomes durable, what is considered
fresh, how skills are surfaced, and how much of the user's world the harness can
see. Those are meaningful harness-philosophy choices.

## Core Boundary

Context providers are not the context builder.

The provider decides what context candidates exist:

- discover sources
- retrieve relevant material
- label candidate slot, authority, confidence, scope, and source semantics
- attach provenance and invalidation metadata
- choose inline text, references, or pre-rendered blocks when it knows best

The context builder decides how accepted candidates become model input:

- order candidates
- choose final placement
- apply global budget
- partition stable and per-call context for prompt caching
- decide inline versus reference placement when still undecided
- shape final prompt parts or messages
- record what was included, omitted, truncated, dereferenced, or transformed

The runtime/mediator owns invocation mechanics:

- construct command envelopes
- pass session, turn, model-call, tool, path, and permission facts to providers
- route to primary/shadow providers
- validate responses
- append semantic events
- preserve causal ordering

The session service remains separate. It owns transcript truth and continuation
state. A context provider may observe session events or receive transcript
material in a request, but it does not become the canonical transcript unless it
also implements the session capability.

Tool routing and tool execution remain separate capabilities. A context provider
service may also publish model-facing tools, but tool publication and tool
execution are not the base context-provider contract.

## Stable And Per-Call Context

Every model call is built from two logical context layers:

```text
ContextBundle {
  stable
  per_call
}
```

`stable` means context that should remain active beyond the immediate model
call, until session end, scope exit, invalidation, replacement, or explicit
omission by the builder.

`per_call` means context selected for the immediate model call. A user turn can
contain several model calls after tool execution, so this contract uses
`per_call` instead of `per_turn`. "Per-turn context" is acceptable shorthand in
notes, but the contract should be precise.

The first user turn can contain both:

- stable context from startup sources such as identity, project instructions,
  user profile, user preferences, skills, baseline memory, and tool guidance
- per-call context from the current user message, explicit file mentions,
  selected open files, query-specific memory recall, or current error text

Stable does not mean system prompt. A stable candidate may be materialized as:

- cached primary prefix
- session overlay
- scoped workspace overlay
- developer/system/user/tool-adjacent message, depending on model API shape
- reference-only active context
- omitted, with the reason recorded

Late-discovered stable context is allowed. For example, a subdirectory
`AGENTS.md` discovered after a tool touches `backend/src/main.py` can become a
stable scoped candidate inside the same user turn. It should not require
rebuilding an already-cached primary prefix.

## Required Action

The base contract exposes one context action:

```text
context_provider.get_context
```

The action may return stable candidates, per-call candidates, or both. The
request says which layers are desired; the response keeps the layers separate.
This preserves the stable/per-call distinction without forcing two provider
round trips or two independent retrieval passes.

Purpose:
Return context candidates for the requested layers.

Typical stable outputs:

- agent identity or persona
- user profile and durable preferences
- project instructions from known context files
- skill index or loaded skill guidance
- stable workspace facts
- baseline memory
- subdirectory instructions discovered after a path-touching tool call

Typical per-call outputs:

- semantic memory recalled for the current query
- file contents or snippets from an explicit filename mention
- relevant code snippets selected for the current task
- tool-result-derived context that should not persist
- warnings or uncertainty notes needed for this call only

Terminal events:

```text
context_provider.context_provided
context_provider.context_failed
```

## Trigger Ownership

The context provider does not have to infer every trigger from raw transcript
text. The runtime/mediator should pass the trigger explicitly.

For path-touching tools, the tool router/executor or runtime middleware is the
component that knows a path was touched. It should report path evidence to the
context-provider request before the next model call in the same turn when that
output is needed.

Flow:

```text
model emits tool call
runtime validates tool call
tool executor runs tool
tool executor reports touched paths / workdir / refs to runtime
runtime invokes context_provider.get_context(requested_layers=[stable, per_call])
context builder builds the next model-call context
model receives tool result plus any newly materialized context
```

This does not require an async event bus. The mediator may append events such as
`tool.path_touched` and `context_provider.context_provided`, but the live control
flow can remain direct and synchronous when the next model call needs the output.

If the turn ends without another model call, stable candidates discovered from
the tool can still be retained for later calls if the builder accepts them.

## Request Shape

All context-provider commands use the project-wide command-envelope shape:

```text
CommandEnvelope = action + metadata + payload + causality refs
```

Sketch:

```text
ContextRequest {
  request_id
  requested_layers              // stable | per_call | both
  session_id?
  turn_id?
  is_first_turn?

  current_message?              // role + content + origin, not only user text
  transcript_view?              // optional active/current state, not full history by default

  trigger?                      // optional hint: session_start | user_message | tool_result | ...
  runtime?                      // small facts such as cwd and date
  workspace_roots?

  refs?                         // pre-resolved file/url/memory/artifact refs
  touched_paths?                // path evidence from tool args/results/runtime
}
```

The provider should not assume it can read arbitrary paths just because a path
is mentioned. The first contract does not yet define a read-policy payload, but
the mediator/runtime still owns permission enforcement.

`trigger` is optional metadata rather than a required enum. Providers can use it
when it is present, but the contract should not grow a large trigger taxonomy
before real implementations demand it.

`current_message` is intentionally role-aware. A context request may be prompted
by a user message, an assistant/model message, a tool result, or another runtime
event. User text is common, but the shape should not assume the current message
always came from a human.

`transcript_view` should be the active/current continuation state by default,
not the entire transcript. Full-history access is separate and should be
requested deliberately through the session capability when needed.

`touched_paths` is request-time evidence, not a canonical session-record field.
It exists so a runtime/tool executor can ask for same-loop context after a tool
touches a path. If implementation experience does not produce a clear need for
this field, it should be removed rather than promoted into session state.

## Transcript Access

The base context request should not send the full transcript by default.

Reasons:

- size grows without bound
- many context providers only need the current message, refs, touched paths, or
  active continuation state
- full-history access is a policy and privacy decision, not a harmless default
- the session capability already owns canonical transcript reads

When a provider needs broader history, the runtime may pass an optional
`transcript_view`, or the provider may request `session.read` through the
mediator if its advertised surface allows that. In either case, the request
should make the scope explicit: active continuation, recent window, whole
session, redacted view, or provider-specific projection.

Hermes supports this distinction in practice. Automatic memory recall is driven
by the current query:

- the shared `MemoryProvider.prefetch(query, *, session_id="")` surface does
  not take a transcript
- Mem0's `prefetch(query)` recalls for the current question, while `sync_turn`
  builds a two-message user/assistant payload for backend ingestion
- Supermemory's `prefetch(query)` recalls profile/search context for the current
  query, while `sync_turn` buffers current turn pairs and `on_session_end`
  ingests a cleaned full-session conversation
- Hermes' `MemoryManager.sync_all(..., messages=None)` passes an OpenAI-style
  message list only to providers whose `sync_turn` signature explicitly accepts
  `messages`; OpenViking uses that optional path to recover tool-bearing current
  turn messages

That is useful evidence against making full transcript a default input to
context retrieval. Full transcript access exists in Hermes, but as an explicit
write/observer or session-boundary capability, not as normal recall input.

## Response Shape

```text
ContextBundle {
  request_id
  provider_id
  stable[]
  per_call[]
}
```

The output payload of a successful action is the payload of its terminal event.
In a direct-call implementation, the mediator may return that terminal event
synchronously as a convenience.

## Candidate Vocabulary

The shared surface should describe roles, not hardcoded filenames such as
`SOUL.md`, `USER.md`, `AGENTS.md`, `CLAUDE.md`, or `.cursorrules`.

Well-known slots:

- `identity`
- `user_profile`
- `project_instructions`
- `workspace_state`
- `session_fact`
- `memory`
- `skills`
- `referenced_material`
- `file_reference`
- `tool_guidance`

The same slot may appear in either layer:

```text
stable.memory
per_call.memory
stable.project_instructions
per_call.referenced_material
stable.skills
per_call.skills
```

This avoids encoding retrieval strategy into the slot name. A memory backend can
return baseline memory as `stable.memory` and query-selected recall as
`per_call.memory`. A skill provider can return a stable skill index at session
start and a per-call skill body when a specific skill is needed.

Custom slots should be namespaced or provider-scoped when possible:

```text
obsidian.daily_note
company.runbook
hermes.skill_context
```

Custom slot handling is an implementation choice. A generic builder may ignore
unknown custom slots, preserve source-rendered text, treat them as generic
context, or support a provider-specific extension. Providers that rely on custom
slot semantics should either pair with a compatible builder or return enough
source-rendered text and metadata for the selected builder's policy.

## Candidate Shape

```text
ContextCandidate {
  id
  provider_id
  slot

  content?                      // full length content, not normalized -- that is done by the builder
  refs?                         // dereferenceable file/url/memory/artifact refs

  source_kind                   // file | url | memory | transcript | tool | runtime | ...
  trigger                       // echoed/refined request trigger

  provenance
  warnings?
}
```

`content` is source-owned text that should be preserved if accepted.
`refs` point to files, URLs, memory records, artifacts, or other dereferenceable
sources. A provider may return any combination when the combination is useful.

## Provenance

Provenance is required for every candidate. If a candidate is derived from an
external source, provenance must identify that source. If a candidate is derived
only from runtime facts, provenance must say which runtime facts were used.

File-derived provenance should include, when available:

- workspace/root identity
- absolute or workspace-relative path
- read time
- mtime or content hash
- byte/line/char range when partial
- transformation kind: `exact`, `sanitized`, `truncated`, `summarized`,
  `rendered`, or `reference_only`
- whether the provider read the file or only returned a ref

This lets the builder choose inline placement, separate context placement,
reference-only placement, or omission while still recording what the candidate
came from.

## Memory Interaction

Memory can interact with the harness in three ways.

1. Memory as context provider:

```text
get_context(requested_layers=[stable]) -> stable.memory
get_context(requested_layers=[per_call]) -> per_call.memory
```

2. Memory as model-facing tool provider:

```text
publish tools such as memory_search, memory_save, memory_forget
handle model tool calls through the tool router/executor
return normal tool-result payloads
```

3. Memory as observer/write sink:

```text
observe completed turns
extract durable facts
sync to backend
mirror explicit memory writes
```

The base context-provider contract only requires the first behavior. Tool
publication and durable write actions are separate advertised surfaces. A memory
backend may implement all three, but the context builder should not assume that
every context provider can write memory or publish tools.

Durability belongs to the memory backend. Placement belongs to the context
builder. A durable memory source can still produce per-call recall context.

## Skills Interaction

Skills are part of context vocabulary, not an implementation detail of prompt
assembly.

Examples:

- `stable.skills`: compact skill index or always-on skill guidance
- `per_call.skills`: loaded skill body, selected references, or skill-specific
  instructions needed for the immediate call

The provider may know how to discover installed skills, rank applicable skills,
load skill files, or return skill references. The builder decides where and how
to materialize those candidates.

## Side Effects

The base context actions should be observational from the harness point of view.
They may refresh caches, indexes, or file watches, but they should not silently
commit durable semantic state as the result of retrieval.

Stateful providers may expose deliberate actions such as:

- observe turn/session events
- remember or update durable memory
- forget or redact memory
- rebuild or refresh indexes
- publish model-facing tools

Those are separate advertised actions or event reactions, not hidden
consequences of asking for context.

## Failure Semantics

Expected failure modes:

- provider unavailable
- request outside allowed scope
- transcript access denied
- source file missing
- referenced content changed or cannot be validated
- index unavailable
- retrieval timeout
- candidate too large
- candidate cannot be dereferenced
- unsafe source content
- unsupported requested slot
- partial results only
- provider-specific memory/search failure

Failure should be explicit and degradable. A provider may return partial
candidates with warnings or return no candidates. It should not return known
stale content as usable context; it should either revalidate the source, return a
fresh candidate, or report that the source could not be used. It should not
fabricate context to hide retrieval failure. The runtime or context builder
should be able to continue without a provider unless that provider was
configured as required.

## Hermes Lessons

Hermes demonstrates a useful combined provider shape: identity files, project
instructions, user profile, built-in memory snapshots, external memory recall,
plugin context, skills, and tool guidance can all feed model input.

Hermes also shows why stable context and primary-prefix placement are different.
Hermes keeps the system prompt stable for prompt caching, so dynamic memory and
plugin context are injected into the current user message at API-call time.
Subdirectory context files are discovered progressively from path-touching tool
calls and appended to tool results rather than rebuilding the system prompt.

For Frankenstein, the lesson is not to copy Hermes' exact transport choice. A
subdirectory `AGENTS.md` discovered after a tool reads `backend/src/main.py`
should be represented as a stable scoped context candidate, not as semantically
ordinary tool output:

```text
stable.project_instructions {
  source = "backend/AGENTS.md"
  scope = workspace_subtree("backend/")
  trigger = path_touched("backend/src/main.py")
}
```

The builder can then render it as a scoped session overlay, per-call context, a
future cached prefix, or nothing, depending on policy and timing.

## Open Questions

- How strict should the shared candidate-slot vocabulary be?
- Should providers be allowed to return hard placement constraints, or only
  hints?
- Which explicit `transcript_view` scopes should the first contract name:
  active continuation, recent window, whole session, redacted view, or
  provider-specific projection?
- Which provider failures should be terminal versus degradable?
- How should conflicts between stable candidates be represented before the
  context builder places them?
- Should invalidation metadata be required for every file-derived candidate?
- How should source-owned rendered text interact with builder-owned templates?
- Should request-level permission/read policy become part of this contract, or
  stay fully owned by the mediator/runtime?
