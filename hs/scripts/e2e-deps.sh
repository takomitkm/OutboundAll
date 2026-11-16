#!/usr/bin/env bash
# e2e-deps.sh -- download and verify the pinned third-party binaries used by
# hs/internal/hstest (headscale control plane, sing-box userspace endpoint,
# official tailscale client as the controller identity holder).
#
# Every download is hash-verified against a pin recorded here. The pins were
# taken from the release asset digests (GitHub API) / pkgs.tailscale.com
# sidecar on 2026-11-17. Bumping a version = bump the pin in the same commit.
#
# Usage:  bash hs/scripts/e2e-deps.sh
# Output: .e2e-deps/ paths + a paths.env file (source it before `go test`).
set -euo pipefail
cd "$(dirname "$0")/../.."   # repo root

DEPS=.e2e-deps
mkdir -p "$DEPS"

fail() { echo "E2E-DEPS FATAL: $*" >&2; exit 1; }

verify_sha() { # file expected-sha
    actual="$(sha256sum "$1" | awk '{print $1}')"
    if [ "$actual" != "$2" ]; then
        fail "sha256 mismatch for $1: expected $2, got $actual"
    fi
    echo "E2E-DEPS ok: $1 ($actual)"
}

# --- headscale v0.29.7 (linux/amd64, bare binary asset) ---------------------
HS_VER=0.29.7
HS_SHA=e827fc9b12d958bc95b081c4401dd63f8e0471d025b45b508535a920532150a8
HS_BIN="$DEPS/headscale"
if [ ! -x "$HS_BIN" ]; then
    curl -fsSL --retry 3 -o "$HS_BIN" \
        "https://github.com/juanfont/headscale/releases/download/v${HS_VER}/headscale_${HS_VER}_linux_amd64"
    verify_sha "$HS_BIN" "$HS_SHA"
    chmod +x "$HS_BIN"
fi

# --- sing-box v1.14.4 (linux/amd64 glibc tarball) ----------------------------
SB_VER=1.14.4
SB_SHA=699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968
SB_TGZ="$DEPS/sing-box-${SB_VER}-linux-amd64.tar.gz"
if [ ! -x "$DEPS/sing-box/sing-box" ]; then
    curl -fsSL --retry 3 -o "$SB_TGZ" \
        "https://github.com/SagerNet/sing-box/releases/download/v${SB_VER}/sing-box-${SB_VER}-linux-amd64.tar.gz"
    verify_sha "$SB_TGZ" "$SB_SHA"
    mkdir -p "$DEPS/sing-box"
    tar -xzf "$SB_TGZ" -C "$DEPS/sing-box" --strip-components=1
fi
SB_BIN="$DEPS/sing-box/sing-box"

# --- tailscale v1.104.7 (linux/amd64, official client for the controller) ---
TS_VER=1.104.7
TS_SHA=e56693ff74a64f2591b7e819c280635a100aae1ddfb0ef058fcd052b7cd8d16c
TS_TGZ="$DEPS/tailscale_${TS_VER}_amd64.tgz"
if [ ! -x "$DEPS/tailscale/tailscale" ]; then
    curl -fsSL --retry 3 -o "$TS_TGZ" \
        "https://pkgs.tailscale.com/stable/tailscale_${TS_VER}_amd64.tgz"
    verify_sha "$TS_TGZ" "$TS_SHA"
    mkdir -p "$DEPS/tailscale"
    tar -xzf "$TS_TGZ" -C "$DEPS/tailscale" --strip-components=1
fi
TS_BIN="$DEPS/tailscale/tailscale"
TSD_BIN="$DEPS/tailscale/tailscaled"

{
    echo "export HS_BIN=$(pwd)/$HS_BIN"
    echo "export SB_BIN=$(pwd)/$SB_BIN"
    echo "export TS_BIN=$(pwd)/$TS_BIN"
    echo "export TSD_BIN=$(pwd)/$TSD_BIN"
} > "$DEPS/paths.env"

echo "E2E-DEPS ok: all pinned deps present under $DEPS"
