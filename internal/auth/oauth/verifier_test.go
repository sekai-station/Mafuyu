package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/settings"
)

func TestJWTValidationAndJWKSFailure(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var status atomic.Int32
	var requests atomic.Int32
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if status.Load() != 200 {
			w.WriteHeader(int(status.Load()))
			return
		}
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key-1", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer server.Close()
	cfg := settings.OAuth{Enabled: true, Mode: "jwt", Issuer: "https://issuer.example", Audience: "mafuyu", JWKSURL: server.URL}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(fields map[string]any, signingKey any, alg jose.SignatureAlgorithm) string {
		t.Helper()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: jose.JSONWebKey{Key: signingKey, KeyID: "key-1"}}, (&jose.SignerOptions{}).WithType("at+jwt"))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := signer.Sign(data)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	base := func() map[string]any {
		return map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "oauth-collector", "exp": time.Now().Add(time.Minute).Unix(), "scope": "rooms:submit"}
	}
	raw := sign(base(), key, jose.RS256)
	if name, err := v.Verify(context.Background(), raw); err != nil || name != "oauth-collector" {
		t.Fatalf("valid access token rejected: %q %v", name, err)
	}
	before := requests.Load()
	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("cached key fetched again")
	}
	for _, tc := range []struct {
		field    string
		value    any
		expected error
	}{
		{"iss", "https://other.example", credential.ErrUnauthorized},
		{"aud", "other-api", credential.ErrUnauthorized},
		{"exp", time.Now().Add(-time.Minute).Unix(), credential.ErrUnauthorized},
		{"nbf", time.Now().Add(time.Minute).Unix(), credential.ErrUnauthorized},
		{"sub", "", credential.ErrUnauthorized},
		{"scope", "rooms:read", credential.ErrForbidden},
	} {
		fields := base()
		fields[tc.field] = tc.value
		if _, err := v.Verify(context.Background(), sign(fields, key, jose.RS256)); !errors.Is(err, tc.expected) {
			t.Fatalf("%s: expected %v, got %v", tc.field, tc.expected, err)
		}
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), sign(base(), otherKey, jose.RS256)); !errors.Is(err, credential.ErrUnauthorized) {
		t.Fatalf("bad signature accepted: %v", err)
	}
	if _, err := v.Verify(context.Background(), sign(base(), []byte(strings.Repeat("x", 32)), jose.HS256)); !errors.Is(err, credential.ErrUnauthorized) {
		t.Fatalf("HMAC algorithm accepted: %v", err)
	}
	status.Store(503)
	fresh, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Verify(context.Background(), raw); !errors.Is(err, credential.ErrUnavailable) {
		t.Fatalf("JWKS failure should be unavailable: %v", err)
	}
}

func TestIntrospection(t *testing.T) {
	t.Setenv("OAUTH_TEST_SECRET", "client-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "mafuyu-client" || secret != "client-secret" || r.Method != "POST" {
			t.Error("invalid introspection request")
			w.WriteHeader(401)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.Form.Get("token_type_hint") != "access_token" {
			t.Error("missing token hint")
		}
		switch r.Form.Get("token") {
		case "outage":
			w.WriteHeader(503)
		case "malformed":
			w.Write([]byte("not-json"))
		case "inactive":
			json.NewEncoder(w).Encode(map[string]any{"active": false})
		default:
			result := map[string]any{"active": true, "sub": "oauth-user", "aud": "mafuyu", "scope": "rooms:submit", "exp": time.Now().Add(time.Minute).Unix()}
			switch r.Form.Get("token") {
			case "wrong-audience":
				result["aud"] = "other"
			case "wrong-issuer":
				result["iss"] = "https://other.example"
			case "expired":
				result["exp"] = time.Now().Add(-time.Minute).Unix()
			case "no-scope":
				result["scope"] = "rooms:read"
			case "no-subject":
				delete(result, "sub")
			}
			json.NewEncoder(w).Encode(result)
		}
	}))
	defer server.Close()
	v, err := New(settings.OAuth{Enabled: true, Mode: "introspection", Issuer: "https://issuer.example", Audience: "mafuyu", IntrospectionURL: server.URL, ClientID: "mafuyu-client", ClientSecretEnv: "OAUTH_TEST_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token    string
		expected error
	}{
		{"valid", nil}, {"inactive", credential.ErrUnauthorized}, {"expired", credential.ErrUnauthorized}, {"wrong-audience", credential.ErrUnauthorized}, {"wrong-issuer", credential.ErrUnauthorized}, {"no-subject", credential.ErrUnauthorized}, {"no-scope", credential.ErrForbidden}, {"outage", credential.ErrUnavailable}, {"malformed", credential.ErrUnavailable},
	} {
		name, err := v.Verify(context.Background(), tc.token)
		if !errors.Is(err, tc.expected) || (err == nil && name != "oauth-user") {
			t.Fatalf("%s: %q %v", tc.token, name, err)
		}
	}
}
