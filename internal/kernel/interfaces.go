package kernel

import (
	"context"

	"frankenstein/internal/contextbuilder"
	"frankenstein/internal/contextprovider"
	"frankenstein/internal/modelinvocation"
	"frankenstein/internal/session"
	"frankenstein/internal/toolinvocation"
)

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
// The kernel creates, resumes, materializes, and mutates sessions; it never
// closes the store during a turn.
type SessionStore interface {
	Create(ctx context.Context, input session.CreateInput) (*session.Session, error)
	Resume(ctx context.Context, input session.ResumeInput) (*session.Session, error)
	Materialize(ctx context.Context, input session.MaterializeInput) (*session.MaterializedSession, error)
	Mutate(ctx context.Context, input session.MutateInput) (*session.Session, error)
}

// ContextBuilder is the kernel's outbound surface to the Context Builder
// capability. The kernel calls estimate, assemble, and prepare in sequence;
// the builder owns the assembly.
type ContextBuilder interface {
	Estimate(req contextbuilder.EstimateRequest) (contextbuilder.Allocation, error)
	Assemble(req contextbuilder.AssembleRequest) (contextbuilder.BuiltPrefix, error)
	Prepare(req contextbuilder.PrepareRequest) (contextbuilder.BuiltContext, error)
}

// ContextProvider is the kernel's outbound surface to the Context Provider
// capability. Exactly one of bundle or failure is non-nil.
type ContextProvider interface {
	Initialize(ctx context.Context, req contextprovider.ContextInitializeRequest) (*contextprovider.ContextBundle, *contextprovider.ContextFailure)
	GetContext(ctx context.Context, req contextprovider.ContextRequest) (*contextprovider.ContextBundle, *contextprovider.ContextFailure)
}
