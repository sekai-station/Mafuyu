package static

import (
	"context"
	"mafuyu/internal/auth/settings"
	"strings"
	"testing"
)

func TestIndependentClientsAndSecrets(t *testing.T) {
	cfg := settings.Static{Enabled: true, Clients: []settings.Client{{Name: "a", Token: "first-key"}, {Name: "b", Token: "second-key"}}}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ token, name string }{{"first-key", "a"}, {"second-key", "b"}, {"wrong", ""}} {
		name, err := v.Verify(context.Background(), tc.token)
		if name != tc.name || (err != nil) != (tc.name == "") {
			t.Fatalf("unexpected identity %q / %v", name, err)
		}
	}
	cfg.Clients[1].Token = "first-key"
	if _, err := New(cfg); err == nil {
		t.Fatal("duplicate tokens accepted")
	}
	for _, invalid := range []string{"", " ", " leading", "trailing ", strings.Repeat("x", 16385)} {
		cfg.Clients[1].Token = invalid
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
