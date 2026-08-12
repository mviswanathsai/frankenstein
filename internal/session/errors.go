package session

import "errors"

var (
	ErrNotFound     = errors.New("session not found")
	ErrDeleted      = errors.New("session deleted")
	ErrInvalidInput = errors.New("invalid session input")
)
