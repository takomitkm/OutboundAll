#!/usr/bin/env bash
# deploy.sh -- reproducible single-VPS deployment for dialhome-hs (Debian 13).
#
# Target: 139.9.101.203 (2c/1G) -- keep it light. Components (ADR-007):
#   headscale 0.29.7 (upstream, pinned)      -- control plane, loopback + SSH tunnels
#   derper v1.102.1 (upstream, CI-built)     -- data plane relay, self-signed IP cert
#                                               + DERP-map fingerprint pin (CertName)
#   hs-controller / hs-deploycheck           -- management plane
#
# Security groups: 22/tcp (tunnels, already open) + 443/tcp (DERP). 3478/udp
# optional (STUN); the shipped DERP map sets STUNPort -1 until it is opened.
#
# Idempotent: re-running upgrades binaries and unit files in place, regenerates
# the DERP map fingerprint from the current cert, and rewrites headscale config
# (previous copy kept as config.yaml.bak-<ver>).
# Run ON the VPS as root:  bash deploy.sh <version-tag>
set -euo pipefail
VER="${1:?usage: deploy.sh <version-tag>}"
VPS_IP="${VPS_IP:-139.9.101.203}"
INSTALL=/usr/local/bin
UNIT=/etc/systemd/system
DERPDIR=/var/lib/dialhome-derp
DERPMAP=/etc/headscale/derp-dialhome.json

echo "== 1) binaries (upload the server package contents to $INSTALL) =="
for b in hs-controller hs-deploycheck derper; do
  if [ ! -f "$INSTALL/$b" ]; then
    echo "missing $INSTALL/$b -- upload it first (dialhome-server package)"
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

echo "== 3) headscale config (authoritative rewrite; backup kept) =="
useradd -r -s /usr/sbin/nologin dialhome 2>/dev/null || true
mkdir -p /etc/headscale /var/lib/headscale "$DERPDIR"
chown -R dialhome:dialhome /var/lib/headscale "$DERPDIR"
if [ -f /etc/headscale/config.yaml ]; then
  cp /etc/headscale/config.yaml "/etc/headscale/config.yaml.bak-$VER"
fi
cat > /etc/headscale/config.yaml <<EOF
server_url: http://127.0.0.1:18080
# loopback only: the control plane reaches clients exclusively through their
# SSH tunnels (-L 18080:127.0.0.1:8080); it must not be internet-reachable
listen_addr: 127.0.0.1:8080
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
  # Data plane relay: self-hosted upstream derper on this VPS (ADR-007 D3/D4).
  # The embedded DERP cannot carry a pinnable cert in its map (map is generated
  # from server_url), so the cert pin (CertName sha256-raw) requires a standalone
  # derper. Tailscale PUBLIC DERP is unreachable from the CN school network
  # (CI-proven, cross-network test) and is a third-party dependency anyway.
  urls: []
  paths:
    - $DERPMAP
  auto_update_enabled: false
  update_freq: 1m
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
mkdir -p /var/lib/dialhome-hs
if [ ! -f /var/lib/dialhome-hs/policy.json ]; then
  echo '{"grants": [], "ssh": []}' > /var/lib/dialhome-hs/policy.json
fi
chown dialhome:dialhome /var/lib/dialhome-hs/policy.json

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
cat > $UNIT/dialhome-derp.service <<EOF
[Unit]
Description=dialhome DERP relay (upstream derper, ADR-007)
After=network-online.target
Wants=network-online.target

[Service]
User=dialhome
Group=dialhome
ExecStart=$INSTALL/derper -hostname=$VPS_IP -certmode=manual -certdir=$DERPDIR \
  -a=:443 -http-port=-1 -home=blank \
  -accept-connection-limit=10 -accept-connection-burst=20
ExecStartPre=+/bin/mkdir -p $DERPDIR
ExecStartPre=+/bin/chown -R dialhome:dialhome $DERPDIR
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$DERPDIR

[Install]
WantedBy=multi-user.target
EOF

echo "== 5) derper first start (creates self-signed IP cert on demand) =="
systemctl daemon-reload
systemctl enable dialhome-derp
systemctl restart dialhome-derp
sleep 3
CERT=$DERPDIR/$VPS_IP.crt
if [ ! -f "$CERT" ]; then
  echo "derper did not create $CERT -- check: journalctl -u dialhome-derp -n 20"
  exit 1
fi

echo "== 6) DERP map with cert fingerprint pin (ADR-007 D4) =="
FP=$(openssl x509 -in "$CERT" -outform DER | sha256sum | cut -d' ' -f1)
cat > $DERPMAP <<EOF
{"Regions":{"900":{"RegionID":900,"RegionCode":"dialhome","RegionName":"dialhome-vps","Nodes":[{"Name":"900a","RegionID":900,"HostName":"$VPS_IP","CertName":"sha256-raw:$FP","DERPPort":443,"STUNPort":-1,"InsecureForTests":false}]}}}
EOF
chmod 644 $DERPMAP
echo "derp map pinned to cert sha256-raw:$FP"

echo "== 7) start control plane =="
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

echo "== 8) leftover cleanup: nc bridge on 18080 (pre-deploy hack) =="
# headscale advertises server_url port 18080 but never listened there; a
# netcat bridge filled the gap during cross-network debugging. Client tunnels
# map 18080 -> 8080 directly, so any nc listener on 18080 is stale.
NC_PID=$(ss -tlnp 2>/dev/null | grep ':18080 ' | grep -oP 'pid=\K[0-9]+' | head -1 || true)
if [ -n "${NC_PID:-}" ]; then
  echo "killing stale 18080 listener (pid $NC_PID: $(ps -o cmd= -p "$NC_PID" 2>/dev/null || true))"
  kill "$NC_PID" 2>/dev/null || true
fi

echo "== 9) availability =="
"$INSTALL/hs-deploycheck" || true
echo "deploy $VER done (version: $INSTALL/*; derp: $DERPMAP; check: hs-deploycheck)"
