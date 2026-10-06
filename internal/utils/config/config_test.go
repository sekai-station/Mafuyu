package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidation(t *testing.T) {
	valid := "port: 8888\nuds_path: /tmp/test.sock\nbackend:\n  unique_time: 0\n  expire_time: 500\n  sqlite_record_keep_day: 14\nv1:\n  break_websocket_time: 6000\n"
	for _, bad := range []string{strings.Replace(valid, "8888", "65536", 1), strings.Replace(valid, "500", "0", 1), strings.Replace(valid, "14", "-1", 1)} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestSendAvatar(t *testing.T) {
	base := "port: 8888\nuds_path: /tmp/test.sock\nbackend:\n  unique_time: 0\n  expire_time: 500\n  sqlite_record_keep_day: 14\nv1:\n  break_websocket_time: 6000\n"
	for _, tc := range []struct {
		extra  string
		hidden bool
	}{
		{"", false},
		{"v2:\n  send_avatar: true\n", false},
		{"v2:\n  send_avatar: false\n", true},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+tc.extra), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.V2.AvatarsHidden() != tc.hidden {
			t.Fatalf("%q: AvatarsHidden() = %v, want %v", tc.extra, cfg.V2.AvatarsHidden(), tc.hidden)
		}
	}
}

func TestOptionalHTTPAuthenticationConfiguration(t *testing.T) {
	base := "port: 8888\nuds_path: /tmp/test.sock\nbackend:\n  expire_time: 500\n  sqlite_record_keep_day: 14\nv1:\n  break_websocket_time: 6000\n"
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"", true},
		{"submission:\n  http:\n    enabled: false\nauth:\n  static:\n    enabled: true\n", true},
		{"submission:\n  http:\n    enabled: true\n", false},
		{"submission:\n  http:\n    enabled: true\nauth:\n  static:\n    enabled: true\n    clients:\n      - name: collector\n        token: config-test-key\n", true},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+tc.extra), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != tc.valid {
			t.Fatalf("unexpected config validation: %v", err)
		}
	}
}

func TestOptionalTransportsAndAPIs(t *testing.T) {
	base := "port: 8888\nbackend:\n  expire_time: 500\n  sqlite_record_keep_day: 14\n"
	for _, tc := range []struct {
		name, extra              string
		uds, v1, v2, http, valid bool
	}{
		{"omitted defaults", "uds_path: /tmp/test.sock\nv1:\n  break_websocket_time: 6000\n", true, true, true, false, true},
		{"all disabled", "submission:\n  uds:\n    enabled: false\n  http:\n    enabled: true\nv1:\n  enabled: false\nv2:\n  enabled: false\n", false, false, false, false, true},
		{"v2 only", "submission:\n  uds:\n    enabled: false\nv1:\n  enabled: false\nv2:\n  enabled: true\n", false, false, true, false, true},
		{"v1 only", "uds_path: /tmp/test.sock\nv1:\n  enabled: true\n  break_websocket_time: 6000\nv2:\n  enabled: false\n", true, true, false, false, true},
		{"enabled UDS requires path", "v1:\n  enabled: false\n", false, false, false, false, false},
		{"enabled v1 requires timeout", "submission:\n  uds:\n    enabled: false\n", false, false, false, false, false},
		{"HTTP with v2 requires auth", "submission:\n  uds:\n    enabled: false\n  http:\n    enabled: true\nv1:\n  enabled: false\n", false, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(base+tc.extra), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected validation: %v", err)
			}
			if !tc.valid {
				return
			}
			if cfg.Submission.UDS.Enabled != tc.uds || cfg.V1.Enabled != tc.v1 || cfg.V2.Enabled != tc.v2 || cfg.HTTPSubmissionEnabled() != tc.http {
				t.Fatalf("unexpected switches: %+v", cfg)
			}
		})
	}
}
