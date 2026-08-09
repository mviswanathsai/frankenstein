# Context Builder v0 Implementation Plan

## Goal

Implement the `context_builder.v0` capability as a Go service with three
actions (`estimate`, `assemble`, `prepare`) and update the `session.v0.2`
Go types so that `prepare` can normalize tool turns. The capability and
service contracts in `docs/` are already reconciled and serve as the spec.

## Cross-cutting concerns

- **Go import cycle.** `toolinvocation` already imports `session` for
  `ContextRef` and `TokenCount`. Do not add an import from `session` back to
  `toolinvocation`. Instead, define a local `session.ToolCall` shape that
  mirrors `toolinvocation.ToolCall` field-for-field. The Context Builder maps
  between the two. A future refactor may extract a shared types package.

- **Observability layering.** Per the updated `AGENTS.md` stance, do not build
  event transport, append-only logs, or complex telemetry in this pass.
  Terminal events are returned as ordinary Go values. The mediator/event-log
  work will be layered later.

- **Language.** Go, consistent with `model_invocation.v0`.

- **Contract stability.** The contracts in `docs/context-builder-capability-contract.md`,
  `docs/context-builder-service-dossier.md`, and `docs/session-capability-contract.md`
  are final for this pass. If implementation reveals an inconsistency, stop
  and ask before changing the contract.

## Ticket 1: Update `SessionRecord` Go types

**Files:** `internal/session/types.go`

**Work**

1. Add a local `ToolCall` shape in the `session` package with the same fields
   as `toolinvocation.ToolCall`:
   - `id`
   - `tool_id` (optional)
   - `definition_revision` (optional)
   - `name`
   - `arguments`
2. Add `ToolCalls []ToolCall `json:"tool_calls,omitempty"`` to `SessionRecord`.
3. Add `CallID string `json:"call_id,omitempty"`` to `SessionRecord`.
4. Make `Text` optional on `SessionRecord` (change from `string` to `*string`
   or add `omitempty` and adjust validation in the service if necessary).
   The contract now says `text` is optional in some cases.

**Acceptance**

- `go build ./internal/session` succeeds.
- `go test ./internal/session` still passes.
- A `SessionRecord` can round-trip through JSON with `tool_calls` and `call_id`.

**Verification**

```bash
go build ./internal/session
go test ./internal/session
```

## Ticket 2: Create Context Builder package skeleton and types

**Files:**

- `internal/contextbuilder/types.go` (new)
- `internal/contextbuilder/service.go` (new, minimal)

**Work**

1. In `types.go`, define:
   - Capability and contract version constants.
   - `EstimateRequest`, `Allocation`, `TranscriptStub`.
   - `AssembleRequest`, `BuiltPrefix`.
   - `PrepareRequest`, `BuiltContext`.
   - `ContextBuilderFailure` and failure-code constants.
   - `NormalizationNote` and action/reason constants.
2. Reuse existing types by import:
   - `ContextBundle` from `internal/contextprovider`.
   - `ToolCatalog` from `internal/toolinvocation`.
   - `SessionRecord` from `internal/session`.
   - `ModelInput`, `ModelMessage` from `internal/modelinvocation`.
3. In `service.go`, define a `Service` struct with stub methods for
   `Estimate`, `Assemble`, and `Prepare` that return `errors.New("not implemented")`.

**Acceptance**

- `go build ./internal/contextbuilder` succeeds.
- Types match the contract shapes (field names, optionality, reuse).

**Verification**

```bash
go build ./internal/contextbuilder
```

## Ticket 3: Implement `context_builder.estimate`

**Files:** `internal/contextbuilder/estimate.go` (new)

**Work**

1. Implement `Estimate(req EstimateRequest) (Allocation, error)`.
2. Validate that `model` and `context_window_tokens` are present.
3. Implement a simple allocation policy:
   - `output_reservation` = max(1024, context_window / 4)
   - `system_prompt_tokens` = min(2048, context_window / 5)
   - `max_tools_tokens` = min(2048, context_window / 5)
   - `max_context_tokens` = min(2048, context_window / 10)
   - `max_transcript_tokens` = context_window - output - system - tools - context
   - Clamp all values to >= 0. If the window is too small, return zeros and
     let the caller decide.
4. Return `-1` for `max_tools_tokens`, `max_context_tokens`, and
   `max_transcript_tokens` if the v0 policy wants to be unopinionated.
   For the reference implementation, return concrete numbers; the `-1`
   behavior must be tested but need not be the default.

**Acceptance**

- Returns a non-negative `system_prompt_tokens`.
- Returns `-1` behavior when configured/opinionated to do so.
- Handles undersized windows without panic.

**Verification**

```bash
go test ./internal/contextbuilder -run TestEstimate
```

## Ticket 4: Implement `context_builder.assemble`

**Files:**

- `internal/contextbuilder/assemble.go` (new)
- `internal/contextbuilder/template.go` (new, optional if template lives in assemble.go)

**Work**

1. Implement `Assemble(req AssembleRequest) (BuiltPrefix, error)`.
2. Validate required fields.
3. Use Go `text/template` with a default embedded template.
4. Template variables:
   - `Model`
   - `Retained` context grouped by slot (from `context_bundles[*].retained.buckets`).
   - `Tools` from `catalog.tools` (name + description only).
5. Render the template into `system_prompt`.
6. Compute `system_prompt_id` as SHA-256 of `system_prompt`, truncated to 16
   hex characters.
7. Ensure byte-stability: identical inputs produce identical output. This
   should fall out naturally if the template is deterministic and inputs are
   ordered consistently.
8. Tool-awareness text must preserve catalog order.

**Default template v0**

A minimal template that emits:

```text
You are a helpful assistant.

<project_instructions>
{{ range .Retained.project_instructions }}
<instruction source="{{ .ID }}">
{{ .Content }}
</instruction>
{{ end }}
</project_instructions>

<available_tools>
{{ range .Tools }}
- {{ .Name }}: {{ .Description }}
{{ end }}
</available_tools>
```

The exact wording is not contract-specified; this is the reference default.

**Acceptance**

- `assemble` returns byte-identical output for identical inputs.
- `system_prompt_id` is a 16-char hex SHA-256 prefix.
- Tool names and descriptions appear in catalog order.
- Retained context slots are addressable in the template.

**Verification**

```bash
go test ./internal/contextbuilder -run TestAssemble
```

## Ticket 5: Implement `context_builder.prepare`

**Files:** `internal/contextbuilder/prepare.go` (new)

**Work**

1. Implement `Prepare(req PrepareRequest) (BuiltContext, error)`.
2. Validate required fields.
3. Normalize `SessionRecord[]` to `ModelMessage[]`:
   - `kind=message, role=user` → `role=user, content=text`
   - `kind=message, role=assistant` → `role=assistant, content=text`,
     `tool_calls` if present
   - `kind=message, role=tool` → `role=tool, call_id=call_id, content=text`
   - `kind=tool_call` → `role=assistant, tool_calls=tool_calls`, optional content
   - `kind=tool_result` → `role=tool, call_id=call_id, content=text`
   - `kind=system_note` → drop or convert to a user message (implementation choice;
     document the choice)
4. Repair broken turns:
   - Drop an assistant `tool_call` record with no matching result (record
     `dropped` + `missing_tool_result`).
   - Synthesize a missing tool result if needed (record `synthesized` +
     `missing_tool_result`).
5. Inject per-call context from `context_bundles[*].per_call.buckets` into the
   current user message. Append XML-delimited blocks after the user's text.
   Example:
   ```text
   <per_call_context slot="memory">
   <candidate id="...">...</candidate>
   </per_call_context>
   ```
6. Keep tool results as separate `tool` messages; do not merge them with user
   text or per-call context.
7. Return `ModelInput { system: prefix.system_prompt, messages: ... }` and
   `normalization` notes.

**Acceptance**

- `prepare` echoes `prefix.system_prompt` verbatim.
- Broken tool turns are dropped or synthesized with notes.
- Per-call context is appended to the current user message.
- Tool results remain separate messages.

**Verification**

```bash
go test ./internal/contextbuilder -run TestPrepare
```

## Ticket 6: Wire the service and add integration tests

**Files:**

- `internal/contextbuilder/service.go`
- `internal/contextbuilder/service_test.go` (new)

**Work**

1. Replace stub methods with real implementations.
2. Add a constructor `NewService(...) *Service`.
3. Add table-driven tests covering:
   - empty/minimal requests
   - full happy-path estimate → assemble → prepare
   - tool-call normalization
   - per-call context injection
   - byte-stable assemble
4. Add `cmd/context-builder/main.go` only if there is a clear need for a
   standalone binary. For v0, the service is likely called in-process by the
   runtime kernel, so a package + tests may be sufficient. If a `cmd` binary
   is added, it should be minimal and consistent with `cmd/session-service`
   and `cmd/context-provider`.

**Acceptance**

- `go test ./internal/contextbuilder` passes.
- Coverage includes all three actions and failure paths.

**Verification**

```bash
go test ./internal/contextbuilder -cover
```

## Ticket 7: Update `docs/project-status.md`

**Files:** `docs/project-status.md`

**Work**

1. Move Context Builder from "active contract work" to a completed/implemented
   state.
2. Note that the runtime-kernel contract is the next dependency for wiring
   the builder into the harness.
3. Keep the open sizing notes (tool invocation / context provider / session
   budget inputs) as items for the next pass.

**Acceptance**

- `docs/project-status.md` reflects the current state accurately.

## Definition of done for the whole plan

- All tickets above are complete.
- `go build ./...` succeeds.
- `go test ./internal/contextbuilder ./internal/session` passes.
- No contract files are modified unless a discrepancy is found and agreed.
- `docs/project-status.md` is updated.

## Commands the orchestrator should run after each ticket

```bash
go build ./...
go test ./internal/contextbuilder ./internal/session
```

## Notes for reviewers

- Focus on whether the Go shapes match the reconciled contracts.
- Flag any place where the builder calls another capability or mutates the
  transcript — those are boundary violations.
- The allocation policy in `estimate` is intentionally simple; it is a
  self-improvement knob, not an optimal heuristic.
