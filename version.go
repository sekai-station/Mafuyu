// Package mafuyu exposes the release version shared by binaries and CI.
package mafuyu

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var releaseVersion string

// Version returns the project's semantic release version.
func Version() string { return strings.TrimSpace(releaseVersion) }
