package shared

import "errors"

// Sentinel errors used across the codebase.
var (
	ErrNotFound     = errors.New("not found")
	ErrRevoked      = errors.New("revoked")
	ErrExpired      = errors.New("expired")
	ErrNoUpdates    = errors.New("no updates provided")
	ErrNATSNotReady = errors.New("NATS connection not initialized")
	ErrInvalidInput = errors.New("invalid input")
)
