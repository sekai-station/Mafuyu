// Package static verifies independently configured API keys.
package static

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/settings"
)

type entry struct {
	name string
	hash [32]byte
}
type Verifier struct{ clients []entry }

func New(cfg settings.Static) (*Verifier, error) {
	if err := (settings.Config{Static: cfg}).Validate(); err != nil {
		return nil, err
	}
	v := &Verifier{}
	seen := make(map[[32]byte]bool)
	for _, client := range cfg.Clients {
		hash := sha256.Sum256([]byte(client.Token))
		if seen[hash] {
			return nil, fmt.Errorf("static clients must have distinct tokens")
		}
		seen[hash] = true
		v.clients = append(v.clients, entry{client.Name, hash})
	}
	return v, nil
}

func (v *Verifier) Verify(ctx context.Context, token string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(token))
	name := ""
	for _, client := range v.clients {
		if subtle.ConstantTimeCompare(hash[:], client.hash[:]) == 1 {
			name = client.name
		}
	}
	if name == "" {
		return "", credential.ErrUnauthorized
	}
	return name, nil
}
