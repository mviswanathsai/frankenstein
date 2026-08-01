package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"frankenstein/internal/contextprovider"
)

func main() {
	if err := run(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, exitErr.Message)
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	defaults := contextprovider.DefaultOptions()
	flags := flag.NewFlagSet("context-provider", flag.ExitOnError)
	providerID := flags.String("provider-id", defaults.ProviderID, "provider id to report in responses")
	maxSourceRead := flags.Int64("max-source-read-bytes", defaults.MaxSourceReadBytes, "maximum accepted source file size in bytes")
	maxCandidateContent := flags.Int64("max-candidate-content-bytes", defaults.MaxCandidateContentBytes, "maximum bytes in each ContextCandidate.content")
	maxBundleContent := flags.Int64("max-bundle-content-bytes", defaults.MaxBundleContentBytes, "maximum emitted candidate-content bytes per bundle")
	maxCandidates := flags.Int("max-candidates", defaults.MaxCandidates, "maximum emitted candidates per bundle")
	maxDirEntries := flags.Int("max-dir-entries", defaults.MaxInspectedDirEntries, "maximum inspected directory entries per request")
	maxWorkers := flags.Int("max-workers", defaults.MaxConcurrentReads, "maximum concurrent source reads")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	args := flags.Args()
	if len(args) == 0 {
		return usageError("missing command")
	}

	opts := contextprovider.Options{
		ProviderID:               *providerID,
		MaxSourceReadBytes:       *maxSourceRead,
		MaxCandidateContentBytes: *maxCandidateContent,
		MaxBundleContentBytes:    *maxBundleContent,
		MaxCandidates:            *maxCandidates,
		MaxInspectedDirEntries:   *maxDirEntries,
		MaxConcurrentReads:       *maxWorkers,
	}

	switch args[0] {
	case "version":
		if len(args) != 1 {
			return usageError("version does not accept arguments")
		}
		info := contextprovider.ContractInfo{
			Capability:      contextprovider.CapabilityName,
			ContractVersion: contextprovider.ContractVersion,
			ProviderID:      contextprovider.NewProvider(opts).Info().ProviderID,
		}
		return encode(os.Stdout, info)
	case "initialize":
		if len(args) != 1 {
			return usageError("initialize does not accept arguments")
		}
		var input contextprovider.ContextInitializeRequest
		if err := decodeRequired(&input); err != nil {
			return err
		}
		provider := contextprovider.NewProvider(opts)
		bundle, failure := provider.Initialize(context.Background(), input)
		if failure != nil {
			_ = encode(os.Stdout, failure)
			return &exitError{Code: 1, Message: failure.Message}
		}
		return encode(os.Stdout, bundle)
	case "get-context":
		if len(args) != 1 {
			return usageError("get-context does not accept arguments")
		}
		var input contextprovider.ContextRequest
		if err := decodeRequired(&input); err != nil {
			return err
		}
		provider := contextprovider.NewProvider(opts)
		bundle, failure := provider.GetContext(context.Background(), input)
		if failure != nil {
			_ = encode(os.Stdout, failure)
			return &exitError{Code: 1, Message: failure.Message}
		}
		return encode(os.Stdout, bundle)
	default:
		return usageError("unknown command: " + args[0])
	}
}

func decodeRequired(out any) error {
	if !stdinHasData() {
		return usageError("json input required")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return usageError("json input required")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return usageError("malformed json input: " + err.Error())
	}
	return nil
}

func stdinHasData() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice == 0
}

func encode(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type exitError struct {
	Code    int
	Message string
}

func (e *exitError) Error() string {
	return e.Message
}

func usageError(message string) error {
	return &exitError{
		Code: 2,
		Message: message + "\n\nusage: context-provider [flags] <version|initialize|get-context>\n\n" +
			"initialize/get-context read JSON from stdin. WorkspaceRoot.path and runtime.cwd must be absolute when supplied. " +
			"Relative refs and touched paths resolve only from runtime.cwd; cwd does not grant filesystem access.",
	}
}
