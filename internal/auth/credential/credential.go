// Package credential defines the contract shared by independent auth providers.
package credential

import (
	"context"
	"errors"
)

type Principal struct {
	Subject  string
	Provider string
}

type Verifier interface {
	Verify(context.Context, string) (string, error)
}

var (
	ErrUnauthorized = errors.New("invalid credentials")
	ErrForbidden    = errors.New("insufficient permissions")
	ErrUnavailable  = errors.New("authentication service unavailable")
)
