package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"frankenstein/internal/session"
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
	flags := flag.NewFlagSet("session-service", flag.ExitOnError)
	dbPath := flags.String("db", "sessions.db", "sqlite database path")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	args := flags.Args()
	if len(args) == 0 {
		return usageError("missing command")
	}

	if args[0] == "version" {
		if len(args) != 1 {
			return usageError("version does not accept a session id")
		}
		return encode(os.Stdout, session.Info())
	}

	ctx := context.Background()
	store, err := session.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	switch args[0] {
	case "create":
		if len(args) != 1 {
			return usageError("create does not accept a session id")
		}
		var input session.CreateInput
		if err := decodeRequired(&input); err != nil {
			return err
		}
		result, err := store.Create(ctx, input)
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
	case "resume":
		if len(args) != 2 {
			return usageError("resume requires session id")
		}
		result, err := store.Resume(ctx, session.ResumeInput{ID: args[1]})
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
	case "read":
		if len(args) != 2 {
			return usageError("read requires session id")
		}
		result, err := store.Read(ctx, session.ReadInput{ID: args[1]})
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
	case "materialize":
		if len(args) != 2 {
			return usageError("materialize requires session id")
		}
		result, err := store.Materialize(ctx, session.MaterializeInput{ID: args[1]})
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
	case "mutate":
		if len(args) != 2 {
			return usageError("mutate requires session id")
		}
		var input session.MutateInput
		if err := decodeRequired(&input); err != nil {
			return err
		}
		input.ID = args[1]
		result, err := store.Mutate(ctx, input)
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
	case "delete":
		if len(args) != 2 {
			return usageError("delete requires session id")
		}
		result, err := store.Delete(ctx, session.DeleteInput{ID: args[1]})
		if err != nil {
			return err
		}
		return encode(os.Stdout, result)
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
	return json.Unmarshal(data, out)
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
		Code:    2,
		Message: message + "\n\nusage: session-service -db sessions.db <version|create|resume|read|materialize|mutate|delete> [session-id]\n\ncreate input: {\"prompt\":\"...\"}",
	}
}
