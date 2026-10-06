package auth

import (
	"context"
	"mafuyu/internal/auth/credential"
)

type Principal = credential.Principal
type principalKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
