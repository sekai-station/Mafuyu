// Package config loads and validates the server configuration from a YAML file.
package config

import (
	"fmt"
	"mafuyu/internal/auth/settings"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level server configuration.
type Config struct {
	Host            string           `yaml:"host"`
	Port            int              `yaml:"port"`
	UDSPath         string           `yaml:"uds_path"`
	LogLevel        string           `yaml:"log_level"`
	LogFile         *string          `yaml:"log_file"`
	LogFileLevel    *string          `yaml:"log_file_level"`
	AnnouncementDir string           `yaml:"announcement_dir"`
	Filter          *FilterConfig    `yaml:"filter"`
	Backend         BackendConfig    `yaml:"backend"`
	V1              V1Config         `yaml:"v1"`
	V2              V2Config         `yaml:"v2"`
	DatabasePath    string           `yaml:"database_path"`
	Submission      SubmissionConfig `yaml:"submission"`
	Auth            settings.Config  `yaml:"auth"`
}

type SubmissionConfig struct {
	HTTP HTTPSubmissionConfig `yaml:"http"`
	UDS  UDSSubmissionConfig  `yaml:"uds"`
}
type UDSSubmissionConfig struct {
	Enabled bool `yaml:"enabled"`
}
type HTTPSubmissionConfig struct {
	Enabled bool `yaml:"enabled"`
}

// FilterConfig controls room content filtering (compiled in with -tags zhcn).
// All fields are optional — an empty or absent filter section disables filtering
// even when the zhcn build tag is set.
type FilterConfig struct {
	Keywords        []string `yaml:"keywords"`          // keyword blacklist matched against room message
	RejectPureDigit *bool    `yaml:"reject_pure_digit"` // reject messages that are entirely digits
}

// BackendConfig controls in-memory store and SQLite persistence behavior.
type BackendConfig struct {
	UniqueTime          int `yaml:"unique_time"`            // seconds before the same room ID can be re-accepted
	ExpireTime          int `yaml:"expire_time"`            // seconds after which a room is evicted from the store
	SQLiteRecordKeepDay int `yaml:"sqlite_record_keep_day"` // how many days of snapshot history to retain
}

// V1Config holds settings specific to the legacy v1 WebSocket protocol.
type V1Config struct {
	Enabled            bool `yaml:"enabled"`
	BreakWebsocketTime int  `yaml:"break_websocket_time"` // seconds before a v1 WS client is force-disconnected
}

// V2Config holds settings for the v2 API (/recent and the /realtime stream).
type V2Config struct {
	Enabled    bool  `yaml:"enabled"`
	SendAvatar *bool `yaml:"send_avatar"` // false: every avatar is sent as null; absent: true
}

// AvatarsHidden reports whether v2 sends every avatar as null.
func (c V2Config) AvatarsHidden() bool {
	return c.SendAvatar != nil && !*c.SendAvatar
}

// HTTPSubmissionEnabled reports whether an enabled API can accept HTTP submissions.
func (c Config) HTTPSubmissionEnabled() bool {
	return c.Submission.HTTP.Enabled && (c.V1.Enabled || c.V2.Enabled)
}

// Load reads and validates YAML. UDS and API versions default to enabled;
// HTTP submission defaults to disabled and LogLevel defaults to "info".
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Config{
		Submission: SubmissionConfig{UDS: UDSSubmissionConfig{Enabled: true}},
		V1:         V1Config{Enabled: true},
		V2:         V2Config{Enabled: true},
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.AnnouncementDir == "" {
		cfg.AnnouncementDir = "announcements"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "data.db"
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if cfg.Submission.UDS.Enabled && cfg.UDSPath == "" {
		return nil, fmt.Errorf("uds_path is required")
	}
	if cfg.Backend.UniqueTime < 0 || cfg.Backend.ExpireTime <= 0 || cfg.Backend.SQLiteRecordKeepDay <= 0 {
		return nil, fmt.Errorf("unique_time must be nonnegative; expire_time and sqlite_record_keep_day must be positive")
	}
	if cfg.V1.Enabled && cfg.V1.BreakWebsocketTime <= 0 {
		return nil, fmt.Errorf("break_websocket_time must be positive when v1 is enabled")
	}
	if cfg.HTTPSubmissionEnabled() {
		if err := cfg.Auth.Validate(); err != nil {
			return nil, fmt.Errorf("auth config: %w", err)
		}
	}
	return &cfg, nil
}
