// Package touchedpath holds TouchedPath, request-time path evidence.
//
// A touched path reports that a file or directory was read, written, listed,
// or executed during a turn. The frontend and tool executor observe the
// touch; the runtime forwards the evidence on the next context-provider
// request so discovery can react to what a tool just accessed.
//
// Touched paths are evidence, not state. They are not a canonical
// session-record field and carry no authorization of their own: a path being
// touched never grants a provider permission to read it. Access remains
// bounded by the workspace roots granted on each request.
//
// This vocabulary is expected to relocate to the gateway package when that
// capability exists. Until then it lives here, owned by neither producer nor
// consumer.
package touchedpath
