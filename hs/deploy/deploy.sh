#!/usr/bin/env bash
# deploy.sh -- reproducible single-VPS deployment for dialhome-hs (Debian 13).
#
# Target: 139.9.101.203 (2c/1G) -- keep it light: 3 Go binaries, headscale,
# caddy NOT included in phase 1 of deployment (plain http on loopback + ssh
# tunnel for admin access; TLS termination documented in OPERATIONS.md).
#
# Idempotent: re-running upgrades binaries and unit files in place.
# Run ON the VPS as root:  bash deploy.sh <version-tag>
set -euo pipefail
VER="${1:?usage: deploy.sh <version-tag>}"
INSTALL=/usr/local/bin
UNIT=/etc/systemd/system

echo "== 1) binaries from GitHub Actions artifacts =="
for b in hs-controller hs-node hs-deploycheck; do
  if [ ! -f "$INSTALL/$b" ]; then
    echo "missing $INSTALL/$b -- upload it first (build-matrix artifact)"
    exit 1
  fi
  chmod 755 "$INSTALL/$b"
done
echo "binaries present"

echo "== 2) headscale binary (pinned 0.29.7) =="
if [ ! -x "$INSTALL/headscale" ]; then
  curl -fsSL --retry 3 -o "$INSTALL/headscale" \
    "https://github.com/juanfont/headscale/releases/download/v0.29.7/headscale_0.29.7_linux_amd64"
  chmod 755 "$INSTALL/headscale"
fi
"$INSTALL/headscale" version

echo "== 3) headscale config =="
mkdir -p /etc/headscale /var/lib/headscale
if [ ! -f /etc/headscale/config.yaml ]; then
  cat > /etc/headscale/config.yaml <<EOF
server_url: http://127.0.0.1:8080
listen_addr: 0.0.0.0:8080
metrics_listen_addr: 127.0.0.1:9090
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: false
  # Data plane relay: Tailscale PUBLIC DERP network (headscale documented
  # usage). The embedded DERP does not work over a plain-http control plane
  # (DERP clients force TLS -- CI-proven "first record does not look like a
  # TLS handshake"), and 139's security group only opens 22/tcp. The control
  # plane rides SSH tunnels (22); the data plane rides the public DERP relay
  # (WireGuard E2E encryption unchanged; 139 carries no data-plane traffic).
  # Standalone DERP + TLS via Caddy is the production upgrade (ADR-001).
  urls:
    - https://controlplane.tailscale.com/derpmap/default
  auto_update_enabled: true
disable_check_updates: true
node:
  expiry: 0
  ephemeral:
    inactivity_timeout: 30m
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true
policy:
  mode: file
  path: /var/lib/dialhome-hs/policy.json
dns:
  magic_dns: true
  base_domain: tailnet.dialhome.internal
  override_local_dns: true
  nameservers:
    global:
      - 1.1.1.1
unix_socket: /var/run/headscale/headscale.sock
log:
  level: info
  format: text
EOF
  # policy lives in the CONTROLLER's data dir: the controller (dialhome
  # user) atomically writes it per authz change (tmp+rename needs directory
  # write permission -- /etc/headscale is root-owned, real-machine failure)
  echo '{"grants": [], "ssh": []}' > /var/lib/dialhome-hs/policy.json
  chown dialhome:dialhome /var/lib/dialhome-hs/policy.json
fi

echo "== 4) systemd units =="
cat > $UNIT/headscale.service <<'EOF'
[Unit]
Description=Headscale control plane
After=network-online.target
Wants=network-online.target

[Service]
User=dialhome
Group=dialhome
ExecStart=/usr/local/bin/headscale serve --config /etc/headscale/config.yaml
ExecStartPre=+/bin/mkdir -p /var/lib/headscale /var/run/headscale
ExecStartPre=+/bin/chown -R dialhome:dialhome /var/lib/headscale /var/run/headscale
RuntimeDirectory=headscale
RuntimeDirectoryMode=0755
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/headscale /run/headscale

[Install]
WantedBy=multi-user.target
EOF
useradd -r -s /usr/sbin/nologin dialhome 2>/dev/null || true
mkdir -p /var/lib/dialhome-hs
chown -R dialhome:dialhome /var/lib/dialhome-hs
cat > $UNIT/hs-controller.service <<EOF
[Unit]
Description=dialhome hs-controller management plane
After=headscale.service
Wants=headscale.service

[Service]
ExecStart=$INSTALL/hs-controller serve --listen 127.0.0.1:8800 \
  --headscale-bin $INSTALL/headscale --headscale-config /etc/headscale/config.yaml \
  --headscale-user ci --server http://127.0.0.1:18080 \
  --policy-file /var/lib/dialhome-hs/policy.json
User=dialhome
Environment=HOME=/var/lib/dialhome-hs
ExecStartPre=/bin/mkdir -p /var/lib/dialhome-hs
Restart=on-failure
RestartSec=5
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

echo "== 5) enable + start =="
systemctl daemon-reload
systemctl enable headscale
systemctl restart headscale
sleep 3
# idempotent: the controller resolves this user at startup
$INSTALL/headscale --config /etc/headscale/config.yaml users create ci 2>/dev/null || true
# init the store AS THE SERVICE USER (H6-class: init as root writes /root,
# serve as dialhome reads /var/lib/dialhome-hs -- split state)
if [ ! -f /var/lib/dialhome-hs/.dialhome-hs/registry.json ]; then
  runuser -u dialhome -- env HOME=/var/lib/dialhome-hs $INSTALL/hs-controller init 2>/dev/null ||     runuser -u dialhome -- env HOME=/var/lib/dialhome-hs $INSTALL/hs-controller init
  systemctl restart hs-controller
fi
systemctl enable hs-controller
systemctl restart hs-controller
sleep 3

echo "== 6) availability =="
"$INSTALL/hs-deploycheck" || true
echo "deploy $VER done (version: $INSTALL/*; check: hs-deploycheck)"
