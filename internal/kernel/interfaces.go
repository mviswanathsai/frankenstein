package kernel

import (
	"context"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

// TurnObserver receives streaming events from the kernel during a turn.
// The kernel calls it at key moments — model output, tool execution,
// turn completion. The frontend (or gateway) implements it.
type TurnObserver interface {
	OnModelContent(delta string)
	OnReasoning(delta string)
	OnToolCallStart(name string, args map[string]any)
	OnToolResult(result toolinvocation.ToolResult)
	OnTurnEnd(exitReason ExitReason, finalContent string)
}

// ToolInvoker is the kernel's outbound surface to the Tool Invocation
// capability. The concrete service satisfies it structurally; the kernel
// never constructs, modifies, or reorders a catalog itself.
type ToolInvoker interface {
	ListTools(ctx context.Context, req toolinvocation.ToolCatalogRequest) (*toolinvocation.ToolCatalogListed, *toolinvocation.ToolCatalogFailure)
	Execute(ctx context.Context, req toolinvocation.ToolExecutionRequest) (*toolinvocation.ToolExecutionResult, *toolinvocation.ToolExecutionFailure)
}

// ModelInvoker is the kernel's outbound surface to the Model Invocation
// capability. Exactly one of result or failure is non-nil.
type ModelInvoker interface {
	Invoke(ctx context.Context, req modelinvocation.ModelInvocationRequest) (*modelinvocation.ModelInvocationResult, *modelinvocation.ModelInvocationFailure)
}

// SessionStore is the kernel's outbound surface to the Session capability.
// The kernel creates and gets sessions, and writes through the dedicated
// record and state actions; it never closes the store during a turn.
type SessionStore interface {
	Create(ctx context.Context, input session.CreateInput) (*session.CreateResult, error)
	Get(ctx context.Context, input session.GetInput) (*session.Session, error)
	Delete(ctx context.Context, input session.DeleteInput) (*session.DeleteResult, error)
	WriteMessage(ctx context.Context, input session.WriteMessageInput) (*session.WriteResult, error)
	WriteToolCall(ctx context.Context, input session.WriteToolCallInput) (*session.WriteResult, error)
	WriteToolResult(ctx context.Context, input session.WriteToolResultInput) (*session.WriteResult, error)
	WriteSystemNote(ctx context.Context, input session.WriteSystemNoteInput) (*session.WriteResult, error)
	WriteRecord(ctx context.Context, input session.WriteRecordInput) (*session.WriteResult, error)
	SetMetadata(ctx context.Context, input session.SetMetadataInput) (*session.SetResult, error)
	SetUsage(ctx context.Context, input session.SetUsageInput) (*session.SetResult, error)
}

// ContextBuilder is the kernel's outbound surface to the Context Builder
// capability. The kernel calls assemble (with the frozen stable candidates)
// and prepare (with per-turn dynamic responses); estimate is a
// builder-owned surface the v0 kernel does not call.
type ContextBuilder interface {
	Assemble(req contextbuilder.AssembleRequest) (contextbuilder.BuiltPrefix, error)
	Prepare(req contextbuilder.PrepareRequest) (contextbuilder.BuiltContext, error)
}

// ContextProvider is the kernel's outbound surface to the Context Provider
// capability. GetStableContext runs once per session and its response is
// frozen into session metadata; GetDynamicContext runs per turn. Exactly one
// of response or failure is non-nil.
type ContextProvider interface {
	GetDynamicContext(ctx context.Context, req contextprovider.DynamicContextRequest) (*contextprovider.ContextResponse, *contextprovider.ContextFailure)
	GetStableContext(ctx context.Context, req contextprovider.StableContextRequest) (*contextprovider.ContextResponse, *contextprovider.ContextFailure)
}
