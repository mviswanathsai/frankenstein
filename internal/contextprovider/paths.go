package contextprovider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pathAuthorizer struct {
	roots []authorizedRoot
}

type authorizedRoot struct {
	path string
}

type runtimeScope struct {
	cwd string
}

type resolvedPath struct {
	raw       string
	lexical   string
	canonical string
	exists    bool
	isDir     bool
	isFile    bool
	info      os.FileInfo
	code      string
	err       error
}

func newAuthorizer(roots []WorkspaceRoot) (*pathAuthorizer, error) {
	canonical := make([]string, 0, len(roots))
	for _, root := range roots {
		raw := strings.TrimSpace(root.Path)
		if raw == "" {
			return nil, &providerError{code: FailureInvalidWorkspaceRoot, message: "workspace root path is required"}
		}
		if !filepath.IsAbs(raw) {
			return nil, &providerError{
				code:    FailureInvalidRelativeWorkspaceRoot,
				message: fmt.Sprintf("workspace root path must be absolute: %s", raw),
			}
		}
		cleaned := filepath.Clean(raw)
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, &providerError{code: FailurePermissionDenied, message: fmt.Sprintf("workspace root cannot be resolved: %s", cleaned)}
			}
			return nil, &providerError{code: FailureInvalidWorkspaceRoot, message: fmt.Sprintf("workspace root cannot be resolved: %s", cleaned)}
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil, &providerError{code: FailurePermissionDenied, message: fmt.Sprintf("workspace root cannot be inspected: %s", cleaned)}
			}
			return nil, &providerError{code: FailureInvalidWorkspaceRoot, message: fmt.Sprintf("workspace root cannot be inspected: %s", cleaned)}
		}
		if !info.IsDir() {
			return nil, &providerError{code: FailureInvalidWorkspaceRoot, message: fmt.Sprintf("workspace root is not a directory: %s", cleaned)}
		}
		canonical = append(canonical, filepath.Clean(resolved))
	}

	sort.Slice(canonical, func(i, j int) bool {
		if len(canonical[i]) == len(canonical[j]) {
			return canonical[i] < canonical[j]
		}
		return len(canonical[i]) < len(canonical[j])
	})

	dedup := make([]authorizedRoot, 0, len(canonical))
	for _, root := range canonical {
		duplicate := false
		for _, existing := range dedup {
			if existing.path == root || pathContains(existing.path, root) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			dedup = append(dedup, authorizedRoot{path: root})
		}
	}
	return &pathAuthorizer{roots: dedup}, nil
}

func validateRuntime(runtime *RuntimeFacts) (runtimeScope, error) {
	if runtime == nil || strings.TrimSpace(runtime.CWD) == "" {
		return runtimeScope{}, nil
	}
	cwd := strings.TrimSpace(runtime.CWD)
	if !filepath.IsAbs(cwd) {
		return runtimeScope{}, &providerError{
			code:    FailureInvalidRelativeCWD,
			message: fmt.Sprintf("runtime.cwd must be absolute when supplied: %s", cwd),
		}
	}
	return runtimeScope{cwd: filepath.Clean(cwd)}, nil
}

func (a *pathAuthorizer) empty() bool {
	return a == nil || len(a.roots) == 0
}

func (a *pathAuthorizer) isAuthorized(canonical string) bool {
	if a.empty() {
		return false
	}
	cleaned := filepath.Clean(canonical)
	for _, root := range a.roots {
		if cleaned == root.path || pathContains(root.path, cleaned) {
			return true
		}
	}
	return false
}

func (a *pathAuthorizer) nearestRoot(canonical string) (string, bool) {
	if a.empty() {
		return "", false
	}
	cleaned := filepath.Clean(canonical)
	best := ""
	for _, root := range a.roots {
		if cleaned == root.path || pathContains(root.path, cleaned) {
			if len(root.path) > len(best) {
				best = root.path
			}
		}
	}
	return best, best != ""
}

func (a *pathAuthorizer) canonicalizeExisting(path string) (string, string, error) {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", FailurePermissionDenied, err
		}
		return "", FailureSourceMissing, err
	}
	canonical := filepath.Clean(resolved)
	if a.isAuthorized(canonical) {
		return canonical, "", nil
	}
	if a.isAuthorized(cleaned) {
		return "", FailureSymlinkEscape, fmt.Errorf("resolved path escapes workspace roots: %s -> %s", cleaned, canonical)
	}
	return "", FailurePathOutsideWorkspaceRoots, fmt.Errorf("path is outside workspace roots: %s", cleaned)
}

func (a *pathAuthorizer) resolveInput(raw string, scope runtimeScope) resolvedPath {
	cleanRaw := strings.TrimSpace(raw)
	if cleanRaw == "" {
		return resolvedPath{raw: raw, code: FailureSourceMissing, err: fmt.Errorf("path is required")}
	}

	var lexical string
	if filepath.IsAbs(cleanRaw) {
		lexical = filepath.Clean(cleanRaw)
	} else {
		if scope.cwd == "" {
			return resolvedPath{raw: raw, code: FailureMissingCWDForRelativePath, err: fmt.Errorf("relative path cannot be resolved without runtime.cwd: %s", cleanRaw)}
		}
		lexical = filepath.Clean(filepath.Join(scope.cwd, filepath.Clean(cleanRaw)))
	}

	canonical, code, err := a.canonicalizeExisting(lexical)
	if err != nil {
		if code == FailureSourceMissing {
			return a.resolveNonexistent(raw, lexical)
		}
		return resolvedPath{raw: raw, lexical: lexical, code: code, err: err}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		code := FailureSourceMissing
		if errors.Is(err, os.ErrPermission) {
			code = FailurePermissionDenied
		}
		return resolvedPath{raw: raw, lexical: lexical, canonical: canonical, code: code, err: err}
	}
	return resolvedPath{
		raw:       raw,
		lexical:   lexical,
		canonical: canonical,
		exists:    true,
		isDir:     info.IsDir(),
		isFile:    info.Mode().IsRegular(),
		info:      info,
	}
}

func (a *pathAuthorizer) resolveNonexistent(raw, lexical string) resolvedPath {
	if a.empty() {
		return resolvedPath{raw: raw, lexical: lexical, canonical: lexical, code: FailurePathOutsideWorkspaceRoots, err: fmt.Errorf("no workspace roots authorize path: %s", lexical)}
	}
	if !a.lexicalAuthorized(lexical) {
		if _, ok, _, err := a.existingParent(lexical); ok && err == nil {
			return resolvedPath{raw: raw, lexical: lexical, canonical: filepath.Clean(lexical), exists: false}
		}
		return resolvedPath{raw: raw, lexical: lexical, canonical: lexical, code: FailurePathOutsideWorkspaceRoots, err: fmt.Errorf("path is outside workspace roots: %s", lexical)}
	}
	return resolvedPath{raw: raw, lexical: lexical, canonical: filepath.Clean(lexical), exists: false}
}

func (a *pathAuthorizer) lexicalAuthorized(path string) bool {
	cleaned := filepath.Clean(path)
	for _, root := range a.roots {
		if cleaned == root.path || pathContains(root.path, cleaned) {
			return true
		}
	}
	return false
}

func (a *pathAuthorizer) existingParent(path string) (string, bool, string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				current = filepath.Dir(current)
				continue
			}
			canonical, code, err := a.canonicalizeExisting(current)
			if err != nil {
				return "", false, code, err
			}
			return canonical, true, "", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			code := FailureSourceMissing
			if errors.Is(err, os.ErrPermission) {
				code = FailurePermissionDenied
			}
			return "", false, code, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, FailureSourceMissing, fmt.Errorf("no existing parent for %s", path)
		}
		current = parent
	}
}

func pathContains(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (a *pathAuthorizer) authorizedCWD(scope runtimeScope) (string, bool) {
	if scope.cwd == "" {
		return "", false
	}
	canonical, _, err := a.canonicalizeExisting(scope.cwd)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return canonical, true
}
