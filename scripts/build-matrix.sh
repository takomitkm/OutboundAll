#!/usr/bin/env bash
# Build the OutboundAll single binary for every supported platform and emit SHA256SUMS.
# Usage: scripts/build-matrix.sh [VERSION]
#   VERSION defaults to `git describe --tags --always --dirty`.
# Output: dist/OutboundAll-<os>-<arch>[.exe] + dist/SHA256SUMS
set -euo pipefail

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
OUT="dist"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}"

# platforms: windows + linux + darwin, both arches (see docs/SSH_BACKEND_MATRIX.md;
# macOS is UNVERIFIED at runtime, but the binary must still build for the platform)
MATRIX="
windows/amd64
windows/arm64
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
"

mkdir -p "$OUT"
rm -f "$OUT"/OutboundAll-* "$OUT/SHA256SUMS"

echo "building OutboundAll ${VERSION} (${COMMIT})"
while read -r target; do
  [ -z "$target" ] && continue
  os="${target%/*}"
  arch="${target#*/}"
  name="OutboundAll-${os}-${arch}"
  [ "$os" = "windows" ] && name="${name}.exe"
  echo "  -> ${name}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/OutboundAll
done <<< "$MATRIX"

# checksums (portable: sha256sum on linux, shasum on macOS)
(
  cd "$OUT"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum OutboundAll-* > SHA256SUMS
  else
    shasum -a 256 OutboundAll-* > SHA256SUMS
  fi
  echo "--- wrote $OUT/SHA256SUMS ---"
  cat SHA256SUMS
)
