#!/usr/bin/env bash
set -euo pipefail

arch="${1:?usage: package-release.sh amd64|arm64 [build-tags] [edition]}"
tags="${2:-}"
edition="${3:-mafuyu}"
case "$arch" in amd64|arm64) ;; *) echo "Unsupported architecture: $arch" >&2; exit 1 ;; esac
case "$edition" in mafuyu|mafuyu-zhcn) ;; *) echo "Unsupported edition: $edition" >&2; exit 1 ;; esac
version="$(tr -d '\r\n' < VERSION)"
base="${edition}_${version}_linux_${arch}"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "dist"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -tags="$tags" -trimpath -ldflags="-s -w" -o "$stage/mafuyu" ./cmd/server
cp VERSION config.yaml README.md API.md LICENSE "$stage/"
mkdir -p "$stage/announcements"
tar -C "$stage" -czf "dist/${base}.tar.gz" .
(cd dist && sha256sum "${base}.tar.gz" > "${base}.sha256")
