package contextprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"frankenstein/internal/session"
)

const (
	priorityExplicitRef          = 1
	priorityDirectTouched        = 2
	priorityInstructionsIdentity = 3
	priorityProfileMemory        = 4
	priorityNativeRules          = 5
	prioritySkillIndex           = 6
	priorityOptionalDiscovered   = 7
	priorityUnknownOptional      = 8
)

// destination is the internal classification of one source into the flat
// response: which slot convention it carries, how strongly the provider
// prefers it, and whether it must be accounted for against an explicit
// input ref.
type destination struct {
	Slot     string
	Priority int
	Explicit bool
	RefLabel string
}

func newDestination(slot string, priority int, explicit bool, label string) destination {
	if slot == "" {
		slot = SlotUnknown
	}
	return destination{Slot: slot, Priority: priority, Explicit: explicit, RefLabel: label}
}

type sourceSpec struct {
	Path         string
	Kind         sourceKind
	Label        string
	Adapter      string
	Refs         []session.ContextRef
	Destinations []destination
	Optional     bool
	Indexable    bool
	Order        int
}

type syntheticSpec struct {
	Candidate   ContextCandidate
	Destination destination
	Order       int
}

type candidateOccurrence struct {
	Candidate ContextCandidate
	Path      string
	Priority  int
	Order     int
	Explicit  bool
	RefLabel  string
	IndexSpec *sourceSpec
}

func buildFileContent(spec sourceSpec, read sourceRead, opts Options) (string, string, error) {
	header := fileHeader(spec)
	marker := truncationMarker(spec.Path)
	body := read.content
	if body == "" && !read.truncated {
		body = "(empty file)\n"
	}
	content, truncated := composeLimitedContent(header, body, marker, read.truncated, opts.MaxCandidateContentBytes)
	if strings.TrimSpace(content) == "" {
		return "", FailureCandidateTooLarge, fmt.Errorf("candidate content is empty after limit enforcement")
	}
	if int64(len([]byte(content))) > opts.MaxCandidateContentBytes {
		return "", FailureCandidateTooLarge, fmt.Errorf("candidate content exceeds max candidate content bytes")
	}
	if truncated && !strings.Contains(content, marker) {
		return "", FailureCandidateTooLarge, fmt.Errorf("truncation marker could not fit")
	}
	return content, "", nil
}

func fileHeader(spec sourceSpec) string {
	label := strings.TrimSpace(spec.Label)
	if label == "" {
		label = filepath.Base(spec.Path)
	}
	source := spec.Path
	if spec.Adapter != "" {
		return fmt.Sprintf("## %s\nSource: %s\nAdapter: %s\n\n", label, source, spec.Adapter)
	}
	return fmt.Sprintf("## %s\nSource: %s\n\n", label, source)
}

func truncationMarker(path string) string {
	return fmt.Sprintf("\n\n[TRUNCATED: %s exceeds the candidate content limit; content is incomplete. Read the source directly for complete content.]\n", path)
}

func composeLimitedContent(header, body, marker string, sourceTruncated bool, maxBytes int64) (string, bool) {
	if maxBytes <= 0 {
		return "", true
	}
	headerBytes := len([]byte(header))
	bodyBytes := len([]byte(body))
	if !sourceTruncated && int64(headerBytes+bodyBytes) <= maxBytes {
		return header + body, false
	}
	markerBytes := len([]byte(marker))
	available := maxBytes - int64(headerBytes) - int64(markerBytes)
	if available < 0 {
		available = 0
	}
	trimmed := trimStringBytes(body, available)
	content := header + trimmed + marker
	if int64(len([]byte(content))) > maxBytes {
		content = trimStringBytes(content, maxBytes)
	}
	return content, true
}

func trimStringBytes(value string, maxBytes int64) string {
	if maxBytes <= 0 {
		return ""
	}
	raw := []byte(value)
	if int64(len(raw)) <= maxBytes {
		return value
	}
	raw = raw[:maxBytes]
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}

func readLimitForSpec(spec sourceSpec, opts Options) (int64, string, error) {
	header := fileHeader(spec)
	marker := truncationMarker(spec.Path)
	headerBytes := int64(len([]byte(header)))
	maxBodyIfComplete := opts.MaxCandidateContentBytes - headerBytes
	if maxBodyIfComplete <= 0 {
		return 0, FailureCandidateTooLarge, fmt.Errorf("candidate header exceeds max content bytes")
	}
	info, err := os.Stat(spec.Path)
	if err != nil {
		return 0, classifyOSError(err, FailureSourceMissing), err
	}
	if !info.Mode().IsRegular() {
		return 0, FailureNonRegularSource, fmt.Errorf("source is not a regular file: %s", spec.Path)
	}
	if info.Size() > opts.MaxSourceReadBytes {
		return 0, FailureSourceTooLarge, fmt.Errorf("source exceeds max source read limit: %s", spec.Path)
	}
	if info.Size() <= maxBodyIfComplete {
		return maxBodyIfComplete, "", nil
	}
	limit := opts.MaxCandidateContentBytes - headerBytes - int64(len([]byte(marker)))
	if limit <= 0 {
		return 0, FailureCandidateTooLarge, fmt.Errorf("candidate truncation marker leaves no room for source content")
	}
	return limit, "", nil
}

// fileCandidateID derives a deterministic candidate ID for a file-backed
// candidate. The semantic input is provider identity, slot convention, and
// canonical source path: the same logical candidate keeps its ID across
// responses within a provider lifecycle, across actions, regardless of
// ordering or priority.
func fileCandidateID(providerID, slot, path string) string {
	semantic := providerID + "|" + slot + "|" + path
	return stableID("ctx", semantic)
}

// syntheticCandidateID derives a deterministic candidate ID for synthesized
// (non-file-backed) candidates such as the skills index and directory
// references.
func syntheticCandidateID(providerID, slot, label string) string {
	semantic := providerID + "|" + slot + "|" + label
	return stableID("ctx", semantic)
}

func stableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:])[:32]
}

func sourceRef(path string, label string) session.ContextRef {
	return session.ContextRef{
		Kind:   "file",
		Target: path,
		Label:  label,
	}
}

func directoryRef(path string, label string) session.ContextRef {
	return session.ContextRef{
		Kind:   "directory",
		Target: path,
		Label:  label,
	}
}

// dedupeOccurrences sorts occurrences by preference (priority, then request
// order, then ID for determinism) and collapses duplicates by candidate ID,
// keeping the best-ranked occurrence of each.
func dedupeOccurrences(occurrences []candidateOccurrence) []candidateOccurrence {
	sortOccurrences(occurrences)
	seen := map[string]bool{}
	out := make([]candidateOccurrence, 0, len(occurrences))
	for _, occ := range occurrences {
		if seen[occ.Candidate.ID] {
			continue
		}
		seen[occ.Candidate.ID] = true
		out = append(out, occ)
	}
	return out
}

func sortOccurrences(occurrences []candidateOccurrence) {
	sort.SliceStable(occurrences, func(i, j int) bool {
		left := occurrences[i]
		right := occurrences[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return left.Candidate.ID < right.Candidate.ID
	})
}

func contentBytes(candidate ContextCandidate) int64 {
	return int64(len([]byte(candidate.Content)))
}

// buildResponse assembles the flat ContextResponse from occurrences. It
// dedupes by candidate ID, enforces per-candidate and response limits, keeps
// failure entries in accumulation order (input-ref failures arrive in input
// order), and reports the canonical paths of candidates that were actually
// emitted so callers can maintain lifecycle state such as the stable set.
func buildResponse(requestID string, occurrences []candidateOccurrence, failures []string, opts Options) (*ContextResponse, []sourceSpec, []string) {
	response := emptyResponse(requestID)
	response.Failures = append(response.Failures, failures...)
	occurrences = dedupeOccurrences(occurrences)

	var emittedBytes int64
	emittedCount := 0
	indexed := make([]sourceSpec, 0)
	emittedPaths := make([]string, 0)
	for _, occ := range occurrences {
		size := contentBytes(occ.Candidate)
		if size <= 0 {
			if occ.Explicit {
				response.Failures = append(response.Failures, refFailure(occ.RefLabel, FailureCandidateTooLarge, "candidate content is empty"))
			}
			continue
		}
		if size > opts.MaxCandidateContentBytes {
			if occ.Explicit {
				response.Failures = append(response.Failures, refFailure(occ.RefLabel, FailureCandidateTooLarge, "candidate exceeds max candidate content bytes"))
			}
			continue
		}
		if emittedCount >= opts.MaxCandidates {
			if occ.Explicit {
				response.Failures = append(response.Failures, refFailure(occ.RefLabel, FailureCandidateCountLimitExceeded, "explicit ref cannot fit within candidate-count limit"))
			}
			continue
		}
		if emittedBytes+size > opts.MaxResponseContentBytes {
			if occ.Explicit {
				response.Failures = append(response.Failures, refFailure(occ.RefLabel, FailureResponseLimitExceeded, "explicit ref cannot fit within response content limit"))
			}
			continue
		}
		response.Candidates = append(response.Candidates, occ.Candidate)
		emittedPaths = append(emittedPaths, occ.Path)
		emittedBytes += size
		emittedCount++
		if occ.IndexSpec != nil && occ.IndexSpec.Indexable {
			indexed = append(indexed, *occ.IndexSpec)
		}
	}

	return response, indexed, emittedPaths
}
