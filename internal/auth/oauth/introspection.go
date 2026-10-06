package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mafuyu/internal/auth/credential"
)

func (v *Verifier) introspect(ctx context.Context, token string) (string, error) {
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	r, err := http.NewRequestWithContext(ctx, "POST", v.cfg.IntrospectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", credential.ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	r.SetBasicAuth(v.cfg.ClientID, v.secret)
	res, err := v.client.Do(r)
	if err != nil {
		return "", credential.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", credential.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return "", credential.ErrUnavailable
	}
	var result struct {
		Active bool `json:"active"`
		claims
	}
	if json.Unmarshal(data, &result) != nil {
		return "", credential.ErrUnavailable
	}
	if !result.Active || (result.Expiry != 0 && result.Expiry <= time.Now().Unix()) || (result.NotBefore != 0 && result.NotBefore > time.Now().Unix()) || !result.Audience.contains(v.cfg.Audience) || (result.Issuer != "" && result.Issuer != v.cfg.Issuer) {
		return "", credential.ErrUnauthorized
	}
	return authorize(result.claims, v.cfg.RequiredScopes)
}
