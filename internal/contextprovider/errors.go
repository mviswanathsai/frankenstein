package contextprovider

import "fmt"

const (
	FailureInvalidRequest               = "invalid_request"
	FailureInvalidRelativeWorkspaceRoot = "invalid_relative_workspace_root"
	FailureInvalidWorkspaceRoot         = "invalid_workspace_root"
	FailureInvalidRelativeCWD           = "invalid_relative_cwd"
	FailureMissingCWDForRelativePath    = "missing_cwd_for_relative_input_path"
	FailurePathOutsideWorkspaceRoots    = "path_outside_workspace_roots"
	FailureSymlinkEscape                = "symlink_escape"
	FailureUnsupportedRefKind           = "unsupported_ref_kind"
	FailureSourceMissing                = "source_missing"
	FailureNonRegularSource             = "non_regular_source"
	FailureSourceTooLarge               = "source_too_large"
	FailureCandidateTooLarge            = "candidate_too_large"
	FailureBundleLimitExceeded          = "bundle_limit_exceeded"
	FailureCandidateCountLimitExceeded  = "candidate_count_limit_exceeded"
	FailureTraversalLimitExceeded       = "traversal_limit_exceeded"
	FailureSourceChangedDuringRead      = "source_changed_during_read"
	FailureContextCanceled              = "context_canceled"
	FailurePermissionDenied             = "filesystem_permission_denied"
)

type providerError struct {
	code      string
	message   string
	retryable *bool
}

func (e *providerError) Error() string {
	if e.message == "" {
		return e.code
	}
	return e.message
}

func terminalFailure(requestID, providerID, code, message string) *ContextFailure {
	return &ContextFailure{
		RequestID:  requestID,
		ProviderID: providerID,
		Code:       code,
		Message:    message,
	}
}

func terminalFailuref(requestID, providerID, code, format string, args ...any) *ContextFailure {
	return terminalFailure(requestID, providerID, code, fmt.Sprintf(format, args...))
}

func refFailure(refLabel, code, message string) string {
	if refLabel == "" {
		return fmt.Sprintf("%s: %s", code, message)
	}
	return fmt.Sprintf("%s: %s: %s", refLabel, code, message)
}

func refFailuref(refLabel, code, format string, args ...any) string {
	return refFailure(refLabel, code, fmt.Sprintf(format, args...))
}
