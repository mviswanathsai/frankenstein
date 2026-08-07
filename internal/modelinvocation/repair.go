package modelinvocation

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"frankenstein/internal/toolinvocation"
)

// RepairArgs attempts escalating repair passes on malformed argument JSON.
// Each pass is tried independently on the original rawArgs. The first pass
// that produces parseable JSON is returned. If all passes fail, returns "{}"
// and a RepairNote of kind RepairArguments.
func RepairArgs(toolCallID string, rawArgs string) (string, *RepairNote) {
	// Pass 1: Loose parse — accept as-is if valid.
	if tryParse(rawArgs) {
		return rawArgs, nil
	}

	// Pass 2: Strip trailing commas before } or ].
	if cleaned := stripTrailingCommas(rawArgs); tryParse(cleaned) {
		return cleaned, nil
	}

	// Pass 3: Balance braces — append missing } and ].
	if balanced := balanceBraces(rawArgs); tryParse(balanced) {
		return balanced, nil
	}

	// Pass 4: Escape literal control characters.
	if escaped := escapeControlChars(rawArgs); tryParse(escaped) {
		return escaped, nil
	}

	return "{}", &RepairNote{
		CallID: toolCallID,
		Kind:   RepairArguments,
	}
}

// RepairToolName normalizes a raw tool name, matches it against the catalog,
// and returns the canonical name. Returns (rawName, nil) when no match is
// found — Tool Invocation's unknown_tool rejection handles the downstream
// error.
func RepairToolName(toolCallID string, rawName string, catalog *toolinvocation.ToolCatalog) (string, *RepairNote) {
	if catalog == nil || len(catalog.Tools) == 0 {
		return rawName, nil
	}

	normalized := normalizeName(rawName)

	// Exact match against catalog definition names.
	for _, tool := range catalog.Tools {
		if tool.Name == normalized {
			return tool.Name, nil
		}
	}

	// Fuzzy match: longest common substring / max name length.
	bestName := ""
	bestScore := 0.0
	for _, tool := range catalog.Tools {
		score := similarityRatio(normalized, tool.Name)
		if score > bestScore && score > 0.6 {
			bestScore = score
			bestName = tool.Name
		}
	}

	if bestName != "" {
		return bestName, &RepairNote{
			CallID:  toolCallID,
			Kind:    RepairName,
			RawName: rawName,
		}
	}

	return rawName, nil
}

// --- private helpers ---

// tryParse returns whether s can be unmarshaled as JSON into an any value.
func tryParse(s string) bool {
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}

// stripTrailingCommas removes commas that appear immediately before } or ]
// (with optional whitespace), respecting JSON string boundaries.
func stripTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	inString := false
	escape := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inString {
			if escape {
				escape = false
			} else {
				if c == '\\' {
					escape = true
				} else if c == '"' {
					inString = false
				}
			}
			b.WriteByte(c)
			continue
		}

		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}

		if c == ',' {
			rest := s[i+1:]
			trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace)
			if len(trimmed) > 0 && (trimmed[0] == '}' || trimmed[0] == ']') {
				continue // skip this trailing comma
			}
		}

		b.WriteByte(c)
	}

	return b.String()
}

// balanceBraces appends missing } and ] to close any unclosed objects and
// arrays. Respects JSON string boundaries.
func balanceBraces(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16) // headroom for appended closers

	type brace byte
	stack := make([]brace, 0, 8)

	inString := false
	escape := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inString {
			if escape {
				escape = false
			} else {
				if c == '\\' {
					escape = true
				} else if c == '"' {
					inString = false
				}
			}
			b.WriteByte(c)
			continue
		}

		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}

		switch c {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == brace(c) {
				stack = stack[:len(stack)-1]
			}
		}

		b.WriteByte(c)
	}

	// Append missing closers in reverse stack order (inner to outer).
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(byte(stack[i]))
	}

	return b.String()
}

// escapeControlChars replaces literal control characters (0x00–0x1F except
// \t, \n, \r) with their JSON \uXXXX escape sequences.
func escapeControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			b.WriteString(fmt.Sprintf("\\u%04x", r))
		} else {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// normalizeName lowercases the name, replaces hyphens and spaces with
// underscores, and strips common suffixes.
func normalizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.TrimSuffix(name, "_tool")
	name = strings.TrimSuffix(name, "_function")
	return name
}

// similarityRatio computes longest-common-substring length divided by the
// longer string's length. Used for fuzzy tool-name matching.
func similarityRatio(a, b string) float64 {
	lcs := longestCommonSubstringLen(a, b)
	if lcs == 0 {
		return 0
	}
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	return float64(lcs) / float64(maxLen)
}

// longestCommonSubstringLen returns the length of the longest contiguous
// substring shared by a and b.
func longestCommonSubstringLen(a, b string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	maxLen := 0

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
				if curr[j] > maxLen {
					maxLen = curr[j]
				}
			} else {
				curr[j] = 0
			}
		}
		prev, curr = curr, prev
	}

	return maxLen
}
