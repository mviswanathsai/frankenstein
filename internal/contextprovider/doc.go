// Package contextprovider implements the context_provider.v0.2 contract.
//
// The contract exposes two read-style actions. GetStableContext is called
// once per session: it sweeps the granted boundary for discoverable
// instruction, identity, profile, memory, and skill material, and the caller
// freezes the result into renderer config. GetDynamicContext is called per
// model call: it dereferences explicit input refs, reacts to touched-path
// evidence with sibling and parent discovery, and re-offers everything it
// has dynamically found earlier in the session from an internal index, so a
// diff-gated renderer receives a complete current offering each time.
//
// Path semantics are deliberately strict. Workspace roots must be absolute.
// Each root is lexically cleaned, resolved through symlinks, canonicalized,
// and deduplicated before use. A root authorizes that directory and all
// descendants recursively; containment checks use filepath.Rel and never
// string-prefix comparison. Authorization comes only from the roots supplied
// on the current request, so a previous request never grants access to a
// later one.
//
// runtime.cwd, when supplied, must be absolute. It is only the base for
// resolving relative refs and touched paths; it does not grant filesystem
// access. Reads under cwd are allowed only when the resolved source is also
// inside a current workspace root. The provider never calls os.Chdir and
// never substitutes process cwd, session cwd, or a workspace root for a
// missing cwd.
//
// Filesystem refs and touched paths may be absolute or relative to runtime.cwd.
// They are cleaned, resolved, canonicalized when the target exists, and then
// checked against current workspace roots. The provider does not expand "~",
// environment variables, globs, PATH entries, basenames, alternate extensions,
// or case variants. Unsupported non-filesystem refs are reported through
// referenced-candidate failure accounting instead of being guessed into paths.
//
// The stable/dynamic partition is enforced structurally: get_stable_context
// remembers the canonical paths it emitted, and get_dynamic_context omits
// discovered candidates backed by those sources. Explicit input refs are
// exempt — every ref must appear among some candidate's refs or in the
// response failures, in input order.
//
// Candidate IDs are deterministic within a provider lifecycle: a hash of
// provider identity, slot convention, and canonical source path (or synthetic
// label). The same logical candidate keeps its ID across responses and across
// both actions; ordering and priority never change identity.
//
// Per-candidate size limits apply to ContextCandidate.content only. Candidate
// IDs, refs, provider IDs, request IDs, failures, JSON field names, and JSON
// escaping overhead are not part of that limit. The provider reserves content
// bytes for its own labels and truncation marker, reads only bytes it can emit
// usefully, and marks partial content explicitly. Complete file transport
// belongs to explicit file-reading tools or a future ranged context-provider
// action, not hidden automatic context discovery.
package contextprovider
