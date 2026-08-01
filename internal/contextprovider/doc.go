// Package contextprovider implements the context_provider.v0.1 contract.
//
// Path semantics are deliberately strict. Workspace roots must be absolute.
// Each root is lexically cleaned, resolved through symlinks, canonicalized, and
// deduplicated before use. A root authorizes that directory and all descendants
// recursively; containment checks use filepath.Rel and never string-prefix
// comparison. Authorization comes only from the roots supplied on the current
// request, so a previous request never grants access to a later one.
//
// runtime.cwd, when supplied, must be absolute. It is only the base for
// resolving relative refs and touched paths; it does not grant filesystem
// access. Reads under cwd are allowed only when the resolved source is also
// inside a current workspace root. The provider never calls os.Chdir and never
// substitutes process cwd, session cwd, or a workspace root for a missing cwd.
//
// Filesystem refs and touched paths may be absolute or relative to runtime.cwd.
// They are cleaned, resolved, canonicalized when the target exists, and then
// checked against current workspace roots. The provider does not expand "~",
// environment variables, globs, PATH entries, basenames, alternate extensions,
// or case variants. Unsupported non-filesystem refs are reported through
// referenced-candidate failure semantics instead of being guessed into paths.
//
// Per-candidate size limits apply to ContextCandidate.content only. Candidate
// IDs, refs, provider IDs, request IDs, bucket names, failures, JSON field
// names, and JSON escaping overhead are not part of that limit. The provider
// reserves content bytes for its own labels and truncation marker, reads only
// bytes it can emit usefully, and marks partial content explicitly. Complete
// file transport belongs to explicit file-reading tools or a future ranged
// context-provider action, not hidden automatic context discovery.
package contextprovider
