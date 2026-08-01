package contextprovider

import "runtime"

const (
	DefaultMaxSourceReadBytes       int64 = 1 << 20
	DefaultMaxCandidateContentBytes int64 = 512 << 10
	DefaultMaxBundleContentBytes    int64 = 4 << 20
	DefaultMaxCandidates            int   = 256
	DefaultMaxInspectedDirEntries   int   = 20_000
)

type Options struct {
	ProviderID string

	MaxSourceReadBytes       int64
	MaxCandidateContentBytes int64
	MaxBundleContentBytes    int64
	MaxCandidates            int
	MaxInspectedDirEntries   int
	MaxConcurrentReads       int
}

func DefaultOptions() Options {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	return Options{
		ProviderID:               DefaultProviderID,
		MaxSourceReadBytes:       DefaultMaxSourceReadBytes,
		MaxCandidateContentBytes: DefaultMaxCandidateContentBytes,
		MaxBundleContentBytes:    DefaultMaxBundleContentBytes,
		MaxCandidates:            DefaultMaxCandidates,
		MaxInspectedDirEntries:   DefaultMaxInspectedDirEntries,
		MaxConcurrentReads:       workers,
	}
}

func normalizeOptions(opts Options) Options {
	defaults := DefaultOptions()
	if opts.ProviderID == "" {
		opts.ProviderID = defaults.ProviderID
	}
	if opts.MaxSourceReadBytes <= 0 {
		opts.MaxSourceReadBytes = defaults.MaxSourceReadBytes
	}
	if opts.MaxCandidateContentBytes <= 0 {
		opts.MaxCandidateContentBytes = defaults.MaxCandidateContentBytes
	}
	if opts.MaxBundleContentBytes <= 0 {
		opts.MaxBundleContentBytes = defaults.MaxBundleContentBytes
	}
	if opts.MaxCandidates <= 0 {
		opts.MaxCandidates = defaults.MaxCandidates
	}
	if opts.MaxInspectedDirEntries <= 0 {
		opts.MaxInspectedDirEntries = defaults.MaxInspectedDirEntries
	}
	if opts.MaxConcurrentReads <= 0 {
		opts.MaxConcurrentReads = defaults.MaxConcurrentReads
	}
	return opts
}
