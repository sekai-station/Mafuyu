//go:build !zhcn

// Package filter provides room content filtering. This file is the default
// no-op implementation used when the "zhcn" build tag is not set.
// Build with -tags zhcn to enable the Chinese-community content filter.
package filter

import (
	"mafuyu/internal/utils/config"
	"mafuyu/internal/utils/model"
)

// Init is a no-op when the zhcn filter is not compiled in.
func Init(_ *config.FilterConfig) {}

// Check always returns true (accept) when the zhcn filter is not compiled in.
func Check(_ *model.Room) bool { return true }
