#!/usr/bin/env bash
# package.sh -- build all hs binaries + upstream derper, assemble role-split
# packages (ADR-007), write SHA256SUMS. Called by ci.yml (artifact) and
# release.yml (tag -> GitHub Release).
#
# Layout produced under dist/:
#   raw binaries      dist/<bin>-<os>-<arch>[.exe]   (upload/inspect flow)
#   server package    dist/packages/OutboundAll-server-linux-amd64.tar.gz
#                       hs-controller hs-deploycheck derper deploy.sh README-server.txt
#   client packages   dist/packages/OutboundAll-client-{windows,linux}-amd64.{zip,tar.gz}
#                       hs-node[.exe] README-client.txt
#   admin packages    dist/packages/OutboundAll-admin-{windows,linux}-amd64.{zip,tar.gz}
#                       hs-admin[.exe] README-admin.txt
#   checksums         dist/packages/SHA256SUMS
#
# Package roles (ADR-007):
#   server = 139 VPS   (headscale downloaded at deploy time; derper bundled)
#   client = 受控节点   (sing-box fetched by `hs-node install-singbox` at setup)
#   admin  = 管理机     (uses the official tailscale client, not bundled)
set -euo pipefail
VERSION="${1:?usage: package.sh <version-string>}"
DERPER_VERSION=v1.102.1  # ADR-007 D3: same base as the sagernet fork (1.102.1)
REPO_ROOT=$(pwd)

mkdir -p dist dist/packages

echo "== hs binaries =="
while IFS='/' read -r os arch cmd out; do
  GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w" \
    -o "dist/$out" "./hs/cmd/$cmd"
done <<'MATRIX'
linux/amd64/hs-controller/hs-controller-linux-amd64
linux/amd64/hs-deploycheck/hs-deploycheck-linux-amd64
linux/amd64/hs-node/hs-node-linux-amd64
linux/amd64/hs-admin/hs-admin-linux-amd64
windows/amd64/hs-node/hs-node-windows-amd64.exe
windows/amd64/hs-admin/hs-admin-windows-amd64.exe
MATRIX

echo "== upstream derper ($DERPER_VERSION, linux-amd64) =="
GOBIN="$PWD/dist" GOOS=linux GOARCH=amd64 \
  go install "tailscale.com/cmd/derper@$DERPER_VERSION"
mv dist/derper dist/derper-linux-amd64

echo "== assemble packages =="
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

# --- server (linux-amd64): plain names inside, ready for /usr/local/bin ---
S="$STAGE/server"
mkdir -p "$S"
cp dist/hs-controller-linux-amd64        "$S/hs-controller"
cp dist/hs-deploycheck-linux-amd64       "$S/hs-deploycheck"
cp dist/derper-linux-amd64               "$S/derper"
cp hs/deploy/deploy.sh                   "$S/deploy.sh"
cat > "$S/README-server.txt" <<EOF
OutboundAll server package ($VERSION)
Upload contents to /usr/local/bin on the VPS, then: bash deploy.sh $VERSION
Requires: openssl, systemd. Security groups: 22/tcp + 443/tcp (3478/udp optional).
See docs/hs/OPERATIONS.md and docs/hs/ADR/007-role-transport-assignment.md.
EOF
tar -czf dist/packages/OutboundAll-server-linux-amd64.tar.gz -C "$S" .

# --- client (受控节点) ---
for os in windows linux; do
  C="$STAGE/client-$os"
  mkdir -p "$C"
  bin="hs-node-linux-amd64"; ext="tar.gz"
  if [ "$os" = windows ]; then bin="hs-node-windows-amd64.exe"; ext="zip"; fi
  cp "dist/$bin" "$C/$(basename "$bin" | sed "s/-$os-amd64//")"
  cat > "$C/README-client.txt" <<EOF
OutboundAll client package ($VERSION, $os-amd64)
受控节点安装包。Run: hs-node enroll ... (see docs/hs/ENROLLMENT.md), then
"hs-node install-singbox" fetches the pinned sing-box (ADR-006 gate applies).
Transport: sing-box embedded tailscale stack, userspace, no admin required.
EOF
  if [ "$ext" = zip ]; then
    (cd "$C" && zip -q -r "$REPO_ROOT/dist/packages/OutboundAll-client-windows-amd64.zip" .)
  else
    tar -czf dist/packages/OutboundAll-client-linux-amd64.tar.gz -C "$C" .
  fi
done

# --- admin (管理机; data plane = official tailscale client, ADR-007 D2) ---
for os in windows linux; do
  A="$STAGE/admin-$os"
  mkdir -p "$A"
  bin="hs-admin-linux-amd64"; ext="tar.gz"
  if [ "$os" = windows ]; then bin="hs-admin-windows-amd64.exe"; ext="zip"; fi
  cp "dist/$bin" "$A/$(basename "$bin" | sed "s/-$os-amd64//")"
  cat > "$A/README-admin.txt" <<EOF
OutboundAll admin package ($VERSION, $os-amd64)
hs-admin talks to the controller API and shells into nodes via the OFFICIAL
tailscale client (install it separately: tailscale up --login-server <url>).
See docs/hs/ENROLLMENT.md (admin machine section) and docs/hs/OPERATIONS.md.
EOF
  if [ "$ext" = zip ]; then
    (cd "$A" && zip -q -r "$REPO_ROOT/dist/packages/OutboundAll-admin-windows-amd64.zip" .)
  else
    tar -czf dist/packages/OutboundAll-admin-linux-amd64.tar.gz -C "$A" .
  fi
done

echo "== checksums =="
( cd dist/packages && sha256sum *.tar.gz *.zip > SHA256SUMS )
cat dist/packages/SHA256SUMS
echo "packages done: $VERSION"
