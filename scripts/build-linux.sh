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

# Build for both linux arches by default (CGO disabled -> static, no libc dep).
# EC2 Graviton (e.g. ec2-host) is arm64; classic Intel/AMD EC2 is amd64.
# Override with: ARCHES="arm64" ./scripts/build-linux.sh
ARCHES="${ARCHES:-amd64 arm64}"
mkdir -p dist
for arch in $ARCHES; do
  echo "==> cross-compiling linux/$arch (CGO disabled)"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -ldflags="-s -w" \
    -o "dist/webhook-tester-linux-$arch" ./cmd/webhook-tester
  ls -lh "dist/webhook-tester-linux-$arch"
  file "dist/webhook-tester-linux-$arch" 2>/dev/null || true
done
echo
echo "Next: scp the matching arch binary to your EC2 host (see DEPLOY.md, Option B)."
echo "  Graviton/arm64:  scp dist/webhook-tester-linux-arm64  ec2-host:/tmp/webhook-tester"
echo "  Intel/amd64:     scp dist/webhook-tester-linux-amd64  <host>:/tmp/webhook-tester"
