package oauth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/settings"
)

type jwtVerifier struct {
	verifier *oidc.IDTokenVerifier
	scopes   []string
}

// trackedKeys classifies JWKS/network failures separately from invalid signatures.
type trackedKeys struct{ keys oidc.KeySet }
type fetchState struct{ err error }
type fetchKey struct{}

func isKeyServiceFailure(err error) bool {
	// RemoteKeySet wraps only remote refresh failures with this prefix; a key
	// mismatch or invalid signature has a distinct verification error.
	return err != nil && strings.HasPrefix(err.Error(), "fetching keys ")
}
func (k trackedKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	payload, err := k.keys.VerifySignature(ctx, raw)
	if state, ok := ctx.Value(fetchKey{}).(*fetchState); ok {
		state.err = err
	}
	return payload, err
}

func newJWT(cfg settings.OAuth, client *http.Client) *jwtVerifier {
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), cfg.JWKSURL)
	verifier := oidc.NewVerifier(cfg.Issuer, trackedKeys{keys}, &oidc.Config{ClientID: cfg.Audience, SupportedSigningAlgs: []string{oidc.RS256, oidc.ES256}})
	return &jwtVerifier{verifier, cfg.RequiredScopes}
}
func (v *jwtVerifier) VerifyIdentity(ctx context.Context, raw string) (credential.Principal, error) {
	state := &fetchState{}
	token, err := v.verifier.Verify(context.WithValue(ctx, fetchKey{}, state), raw)
	if err != nil {
		// Transport/status/JSON errors from JWKS are classified by their wrapped
		// errors below; bad signatures and unknown keys remain unauthorized.
		if isKeyServiceFailure(state.err) {
			return credential.Principal{}, credential.ErrUnavailable
		}
		return credential.Principal{}, credential.ErrUnauthorized
	}
	var c claims
	if err := token.Claims(&c); err != nil || (c.NotBefore != 0 && c.NotBefore > time.Now().Unix()) {
		return credential.Principal{}, credential.ErrUnauthorized
	}
	return identity(c, v.scopes)
}
