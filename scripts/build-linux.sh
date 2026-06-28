#!/usr/bin/env bash
# Build a static linux/amd64 binary for EC2 (embeds the React UI; pure-Go SQLite, no cgo).
# Output: dist/webhook-tester-linux-amd64
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> generating API code"
go generate -skip readme ./...
npm --prefix ./web ci --no-audit --no-fund 2>/dev/null || npm --prefix ./web install
npm --prefix ./web run generate

echo "==> building frontend (embedded into the binary)"
npm --prefix ./web run build

echo "==> cross-compiling linux/amd64 (CGO disabled)"
mkdir -p dist
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o dist/webhook-tester-linux-amd64 ./cmd/webhook-tester

echo "==> done:"
ls -lh dist/webhook-tester-linux-amd64
file dist/webhook-tester-linux-amd64 2>/dev/null || true
echo
echo "Next: scp dist/webhook-tester-linux-amd64 to your EC2 host (see DEPLOY.md, Option B)."
