package touchedpath

import "encoding/json"

// TouchedPath is request-time evidence that a path was accessed. It is
// produced by the frontend or tool executor and carried on a context-provider
// request; it is never persisted as session state.
//
// Source describes where the evidence came from. Common values include
// "tool_argument", "tool_result", and "runtime"; these are descriptive
// strings, not a closed enum.
//
// Operation describes what kind of access occurred. Common values include
// "read", "write", "list", "execute", and "unknown"; these are also
// descriptive strings, not a closed enum.
//
// Metadata carries optional producer-specific detail. No consumer in this
// repository depends on any key.
type TouchedPath struct {
	Path      string                     `json:"path"`
	Source    string                     `json:"source,omitempty"`
	Operation string                     `json:"operation,omitempty"`
	Metadata  map[string]json.RawMessage `json:"metadata,omitempty"`
}
