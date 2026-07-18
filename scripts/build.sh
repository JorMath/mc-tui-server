#!/usr/bin/env bash
# Builds release binaries for Windows and Linux into dist/.
# Usage: ./scripts/build.sh [version]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
mkdir -p dist

build() {
    local goos="$1" goarch="$2" out="$3"
    echo "Building ${out} (${VERSION})..."
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
        go build -trimpath -ldflags "$LDFLAGS" -o "dist/${out}" .
}

build windows amd64 mc-tui-server_windows_amd64.exe
build linux amd64 mc-tui-server_linux_amd64
build linux arm64 mc-tui-server_linux_arm64

echo "Done. Binaries in dist/:"
ls -lh dist/
