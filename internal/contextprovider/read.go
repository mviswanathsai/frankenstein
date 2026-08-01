package contextprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type fileCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	path      string
	content   string
	truncated bool
	readLimit int64
	digest    string
	size      int64
	modTime   time.Time
	mode      os.FileMode
	identity  fileIdentity
}

type fileIdentity struct {
	dev uint64
	ino uint64
	ok  bool
}

type sourceRead struct {
	path      string
	content   string
	truncated bool
	digest    string
	size      int64
	modTime   time.Time
	identity  fileIdentity
}

func newFileCache() *fileCache {
	return &fileCache{entries: map[string]cacheEntry{}}
}

func (c *fileCache) get(path string, readLimit int64, info os.FileInfo) (sourceRead, bool) {
	c.mu.RLock()
	entry, ok := c.entries[path]
	c.mu.RUnlock()
	if !ok {
		return sourceRead{}, false
	}
	if entry.readLimit != readLimit {
		return sourceRead{}, false
	}
	if entry.size != info.Size() || !entry.modTime.Equal(info.ModTime()) || entry.mode != info.Mode() {
		return sourceRead{}, false
	}
	currentIdentity := identityFromInfo(info)
	if entry.identity.ok && currentIdentity.ok && entry.identity != currentIdentity {
		return sourceRead{}, false
	}
	return sourceRead{
		path:      entry.path,
		content:   entry.content,
		truncated: entry.truncated,
		digest:    entry.digest,
		size:      entry.size,
		modTime:   entry.modTime,
		identity:  entry.identity,
	}, true
}

func (c *fileCache) put(read sourceRead, readLimit int64, mode os.FileMode) {
	entry := cacheEntry{
		path:      read.path,
		content:   read.content,
		truncated: read.truncated,
		readLimit: readLimit,
		digest:    read.digest,
		size:      read.size,
		modTime:   read.modTime,
		mode:      mode,
		identity:  read.identity,
	}
	c.mu.Lock()
	c.entries[read.path] = entry
	c.mu.Unlock()
}

func (c *fileCache) drop(path string) {
	c.mu.Lock()
	delete(c.entries, path)
	c.mu.Unlock()
}

func readCachedOrFresh(ctx context.Context, cache *fileCache, path string, readLimit int64, maxSourceBytes int64) (sourceRead, string, error) {
	if err := ctx.Err(); err != nil {
		return sourceRead{}, FailureContextCanceled, err
	}
	if readLimit <= 0 {
		return sourceRead{}, FailureCandidateTooLarge, fmt.Errorf("candidate framing leaves no room for source content")
	}

	info, err := os.Stat(path)
	if err != nil {
		return sourceRead{}, classifyOSError(err, FailureSourceMissing), err
	}
	if !info.Mode().IsRegular() {
		return sourceRead{}, FailureNonRegularSource, fmt.Errorf("source is not a regular file: %s", path)
	}
	if info.Size() > maxSourceBytes {
		return sourceRead{}, FailureSourceTooLarge, fmt.Errorf("source exceeds max source read limit: %s", path)
	}
	if read, ok := cache.get(path, readLimit, info); ok {
		return read, "", nil
	}

	var lastErr error
	var lastCode string
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return sourceRead{}, FailureContextCanceled, err
		}
		read, mode, code, err := readCoherentOnce(ctx, path, readLimit, maxSourceBytes)
		if err == nil {
			cache.put(read, readLimit, mode)
			return read, "", nil
		}
		lastErr = err
		lastCode = code
		if code != FailureSourceChangedDuringRead {
			break
		}
		runtime.Gosched()
	}
	cache.drop(path)
	if lastCode == "" {
		lastCode = FailureSourceChangedDuringRead
	}
	return sourceRead{}, lastCode, lastErr
}

func readCoherentOnce(ctx context.Context, path string, readLimit int64, maxSourceBytes int64) (sourceRead, os.FileMode, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return sourceRead{}, 0, classifyOSError(err, FailureSourceMissing), err
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil {
		return sourceRead{}, 0, classifyOSError(err, FailureSourceMissing), err
	}
	if !before.Mode().IsRegular() {
		return sourceRead{}, 0, FailureNonRegularSource, fmt.Errorf("source is not a regular file: %s", path)
	}
	if before.Size() > maxSourceBytes {
		return sourceRead{}, 0, FailureSourceTooLarge, fmt.Errorf("source exceeds max source read limit: %s", path)
	}

	limited := io.LimitReader(file, readLimit+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return sourceRead{}, 0, classifyOSError(err, FailureSourceMissing), err
	}
	if err := ctx.Err(); err != nil {
		return sourceRead{}, 0, FailureContextCanceled, err
	}

	afterHandle, err := file.Stat()
	if err != nil {
		return sourceRead{}, 0, classifyOSError(err, FailureSourceMissing), err
	}
	afterPath, err := os.Stat(path)
	if err != nil {
		return sourceRead{}, 0, classifyOSError(err, FailureSourceMissing), err
	}
	if !stableRead(before, afterHandle, afterPath) {
		return sourceRead{}, 0, FailureSourceChangedDuringRead, fmt.Errorf("source changed during read: %s", path)
	}
	if afterHandle.Size() > maxSourceBytes {
		return sourceRead{}, 0, FailureSourceTooLarge, fmt.Errorf("source grew beyond max source read limit: %s", path)
	}

	truncated := int64(len(raw)) > readLimit || afterHandle.Size() > readLimit
	if int64(len(raw)) > readLimit {
		raw = raw[:readLimit]
	}
	content := bytesToValidUTF8(raw)
	digestBytes := sha256.Sum256(raw)
	identity := identityFromInfo(afterHandle)
	return sourceRead{
		path:      path,
		content:   content,
		truncated: truncated,
		digest:    hex.EncodeToString(digestBytes[:]),
		size:      afterHandle.Size(),
		modTime:   afterHandle.ModTime(),
		identity:  identity,
	}, afterHandle.Mode(), "", nil
}

func classifyOSError(err error, fallback string) string {
	if errors.Is(err, os.ErrPermission) {
		return FailurePermissionDenied
	}
	if errors.Is(err, os.ErrNotExist) {
		return FailureSourceMissing
	}
	return fallback
}

func bytesToValidUTF8(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	if len(raw) == 0 {
		return ""
	}
	return strings.ToValidUTF8(string(raw), "\uFFFD")
}

func stableRead(before, afterHandle, afterPath os.FileInfo) bool {
	if before.Size() != afterHandle.Size() || !before.ModTime().Equal(afterHandle.ModTime()) || before.Mode() != afterHandle.Mode() {
		return false
	}
	if !afterHandle.ModTime().Equal(afterPath.ModTime()) || afterHandle.Size() != afterPath.Size() || afterHandle.Mode() != afterPath.Mode() {
		return false
	}
	if !os.SameFile(afterHandle, afterPath) {
		return false
	}
	beforeID := identityFromInfo(before)
	afterID := identityFromInfo(afterHandle)
	pathID := identityFromInfo(afterPath)
	if beforeID.ok && afterID.ok && beforeID != afterID {
		return false
	}
	if afterID.ok && pathID.ok && afterID != pathID {
		return false
	}
	return true
}

func identityFromInfo(info os.FileInfo) fileIdentity {
	if info == nil {
		return fileIdentity{}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileIdentity{dev: uint64(stat.Dev), ino: uint64(stat.Ino), ok: true}
	}
	return fileIdentity{}
}
