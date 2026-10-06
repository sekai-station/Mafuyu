// Package oauth verifies access tokens obtained through any OAuth grant,
// including device authorization. Device codes and refresh tokens are not credentials.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/settings"
)

type Verifier struct {
	cfg    settings.OAuth
	client *http.Client
	secret string
	jwt    credential.IdentityVerifier
}

func New(cfg settings.OAuth) (*Verifier, error) {
	if err := (settings.Config{OAuth: cfg}).Validate(); err != nil {
		return nil, err
	}
	if len(cfg.RequiredScopes) == 0 {
		cfg.RequiredScopes = []string{"rooms:submit"}
	}
	v := &Verifier{cfg: cfg, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if cfg.Mode == "jwt" {
		v.jwt = newJWT(cfg, v.client)
	} else {
		v.secret = os.Getenv(cfg.ClientSecretEnv)
		if strings.TrimSpace(v.secret) == "" {
			return nil, fmt.Errorf("OAuth client secret is missing from %s", cfg.ClientSecretEnv)
		}
	}
	return v, nil
}
func (v *Verifier) Verify(ctx context.Context, token string) (string, error) {
	p, err := v.VerifyIdentity(ctx, token)
	return p.Subject, err
}
func (v *Verifier) VerifyIdentity(ctx context.Context, token string) (credential.Principal, error) {
	if v.jwt != nil {
		return v.jwt.VerifyIdentity(ctx, token)
	}
	return v.introspect(ctx, token)
}

type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*a = []string{s}
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*a = values
	return nil
}
func (a audience) contains(want string) bool {
	for _, value := range a {
		if value == want {
			return true
		}
	}
	return false
}

type claims struct {
	ClientID  string          `json:"client_id"`
	Subject   string          `json:"sub"`
	Issuer    string          `json:"iss"`
	Audience  audience        `json:"aud"`
	Expiry    int64           `json:"exp"`
	NotBefore int64           `json:"nbf"`
	Scope     string          `json:"scope"`
	SCP       json.RawMessage `json:"scp"`
}

func authorize(c claims, required []string) (string, error) {
	if strings.TrimSpace(c.Subject) == "" || len(c.Subject) > 128 {
		return "", credential.ErrUnauthorized
	}
	scopes := strings.Fields(c.Scope)
	if len(c.SCP) != 0 {
		var value string
		if json.Unmarshal(c.SCP, &value) == nil {
			scopes = append(scopes, strings.Fields(value)...)
		} else {
			var values []string
			if json.Unmarshal(c.SCP, &values) != nil {
				return "", credential.ErrUnauthorized
			}
			scopes = append(scopes, values...)
		}
	}
	for _, wanted := range required {
		found := false
		for _, scope := range scopes {
			if scope == wanted {
				found = true
				break
			}
		}
		if !found {
			return "", credential.ErrForbidden
		}
	}
	return c.Subject, nil
}

func identity(c claims, required []string) (credential.Principal, error) {
	subject, err := authorize(c, required)
	if err != nil {
		return credential.Principal{}, err
	}
	if c.ClientID != strings.TrimSpace(c.ClientID) || len(c.ClientID) > 128 || strings.ContainsAny(c.ClientID, "\r\n\t") {
		return credential.Principal{}, credential.ErrUnauthorized
	}
	return credential.Principal{Subject: subject, ClientID: c.ClientID}, nil
}
