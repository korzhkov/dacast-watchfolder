#!/usr/bin/env bash
set -euo pipefail

export CGO_ENABLED=0

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

if command -v rsrc >/dev/null 2>&1; then
  if [[ -f ./assets/dacast.ico ]]; then
    cp -f ./assets/dacast.ico ./cmd/watchfolder/app.ico
  fi
  rsrc -manifest ./cmd/watchfolder/app.manifest -ico ./cmd/watchfolder/app.ico -o ./cmd/watchfolder/rsrc.syso
fi

# Default: portable Windows amd64 binary (works from Linux/macOS CI via cross-compile).
GOOS="${GOOS:-windows}"
GOARCH="${GOARCH:-amd64}"
OUT="${OUT:-dacast-watchfolder.exe}"

echo "Building ${OUT} (GOOS=${GOOS} GOARCH=${GOARCH}, CGO_ENABLED=0)"
GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="-H windowsgui -s -w" -o "$OUT" ./cmd/watchfolder
echo "OK: ${OUT}"
