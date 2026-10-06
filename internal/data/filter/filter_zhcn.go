//go:build zhcn

// Package filter provides room content filtering. This file is compiled only
// when the "zhcn" build tag is set (go build -tags zhcn).
//
// Filter rules are loaded from config.FilterConfig at startup via Init().
// If no config is provided, the filter is a no-op.
package filter

import (
	"strings"
	"unicode"

	"mafuyu/internal/utils/config"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

var (
	keywords        []string
	rejectPureDigit bool
	initialized     bool
)

// Init loads filter rules from the config. Must be called once at startup
// before any Check calls. A nil cfg disables all filtering.
func Init(cfg *config.FilterConfig) {
	if cfg == nil {
		return
	}

	// Normalize all keywords to lowercase for case-insensitive matching
	keywords = make([]string, len(cfg.Keywords))
	for i, kw := range cfg.Keywords {
		keywords[i] = strings.ToLower(kw)
	}

	if cfg.RejectPureDigit != nil {
		rejectPureDigit = *cfg.RejectPureDigit
	}

	initialized = len(keywords) > 0 || rejectPureDigit

	if initialized {
		logger.Info.Info("room filter enabled",
			"keywords", len(keywords),
			"reject_pure_digit", rejectPureDigit)
	}
}

// Check returns true if the room should be accepted, false if it should be
// silently dropped. Logs the rejection reason at debug level.
func Check(room *model.Room) bool {
	if !initialized {
		return true
	}

	msg := strings.ToLower(room.Msg)

	for _, kw := range keywords {
		if strings.Contains(msg, kw) {
			logger.Info.Debug("room filtered",
				"id", room.ID, "reason", "keyword", "word", kw)
			return false
		}
	}

	if rejectPureDigit && allDigits(room.Msg) {
		logger.Info.Debug("room filtered",
			"id", room.ID, "reason", "pure_digit")
		return false
	}

	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
