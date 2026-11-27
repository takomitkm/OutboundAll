#!/usr/bin/env bash
# deploy-sshtunnel-selfheal.sh -- from-zero setup of the sshtunnel-selfheal
# reverse tunnel on the RELAY side, then print the HOST-side steps.
#
# This is the missing "preconditions" piece of sshtunnel-selfheal.sh: it makes
# the relay ready (tunnel account + host keypair + authorized_keys line + sshd
# forward check) so that afterwards you only need to drop the key and the
# script on the host and run `start`.
#
# Run this ON THE RELAY (the machine the tunnel dials back into).  Idempotent:
# safe to re-run, it reuses an existing account/key/line instead of duplicating.
#
# usage:
#   bash deploy-sshtunnel-selfheal.sh \
#        --relay-host RELAY_IP_OR_NAME \
#        [--relay-port 22] \
#        [--tunnel-user sshtunnel] \
#        [--tunnel-port 13390] \
#        [--key-dir /root/sshtunnel-selfheal/keys] \
#        [--yes]
#
# What it does (each step prints DONE or SKIP / FAIL):
#   1. sshd check      -- sshd present, AllowTcpForwarding not 'no'
#   2. tunnel account  -- useradd if missing (no login shell; nologin)
#   3. host keypair    -- ssh-keygen -t ed25519 if missing (reuse if present)
#   4. authorized_keys -- append the host pubkey line (tagged, idempotent)
#
# Then it prints:
#   - the HOST-side commands (copy key, edit config, start) for you to run on
#     the NAT'd machine.
#
# Security notes:
#   - the tunnel account has NO login shell (nologin): it exists only to hold
#     the authorized_keys line that allows the -R forward.  It cannot log in
#     interactively.
#   - the generated host keypair is the ONLY credential for the tunnel.  Keep
#     the private key on the host; on the relay only the .pub is used.
#   - the private key stays in --key-dir on the relay until you copy it to the
#     host; after that consider deleting the relay copy (chmod 600 the dir).
set -eu

# ---------- defaults (topology is parameterized; these are safe defaults) ----------
RELAY_HOST=""              # required: the relay's address as seen by the host
RELAY_PORT=22              # relay sshd port the host connects to
TUNNEL_USER=sshtunnel      # dedicated tunnel account on the relay
TUNNEL_PORT=13390          # loopback port opened on the relay (dial-back entry)
KEY_DIR=/root/sshtunnel-selfheal/keys   # where the relay keeps the host keypair
ASSUME_YES=0

usage() {
  sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --relay-host) RELAY_HOST="$2"; shift 2 ;;
    --relay-port) RELAY_PORT="$2"; shift 2 ;;
    --tunnel-user) TUNNEL_USER="$2"; shift 2 ;;
    --tunnel-port) TUNNEL_PORT="$2"; shift 2 ;;
    --key-dir) KEY_DIR="$2"; shift 2 ;;
    --yes) ASSUME_YES=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "unknown option: $1" >&2; usage 1 ;;
  esac
done

[ -n "$RELAY_HOST" ] || { echo "ERROR: --relay-host is required (the address the HOST uses to reach this relay)" >&2; usage 1; }
printf -v _p '%s' "$RELAY_PORT";   case "$_p" in *[!0-9]*|'') echo "ERROR: --relay-port must be a number" >&2; exit 1;; esac
printf -v _p '%s' "$TUNNEL_PORT";  case "$_p" in *[!0-9]*|'') echo "ERROR: --tunnel-port must be a number" >&2; exit 1;; esac

say() { printf '[deploy] %s\n' "$*"; }
fail() { printf '[deploy][FAIL] %s\n' "$*" >&2; exit 1; }

# ---------- 1. sshd check ----------
say "1. sshd present + AllowTcpForwarding"
if command -v sshd >/dev/null 2>&1 || [ -x /usr/sbin/sshd ]; then
  say "   sshd: OK ($(command -v sshd 2>/dev/null || echo /usr/sbin/sshd))"
else
  fail "sshd not found. Install OpenSSH server first (Debian/Ubuntu: apt install openssh-server)"
fi
# AllowTcpForwarding must not be 'no' for -R to work.  Default is 'yes' so we
# only fail if it is explicitly disabled.
ATF="$(sshd -T 2>/dev/null | awk '/^allowtcpforwarding/{print $2}')" || ATF=""
if [ -n "$ATF" ] && [ "$ATF" = "no" ]; then
  fail "AllowTcpForwarding is 'no' in sshd config -- set to 'yes' (or unset) for -R to work"
fi
say "   AllowTcpForwarding: ${ATF:-yes(default)}"

# ---------- 2. tunnel account ----------
say "2. tunnel account '$TUNNEL_USER'"
if id "$TUNNEL_USER" >/dev/null 2>&1; then
  say "   account exists (uid $(id -u "$TUNNEL_USER")) -- reusing"
else
  # no login shell: the account exists only to own the authorized_keys line.
  # useradd without -m so it has no home dir; authorized_keys goes in a
  # dedicated dir we create below.
  useradd --no-create-home --shell /usr/sbin/nologin "$TUNNEL_USER" 2>/dev/null \
    || useradd --no-create-home --shell /sbin/nologin "$TUNNEL_USER"
  say "   account created (uid $(id -u "$TUNNEL_USER")), shell nologin"
fi
TUNNEL_HOME="/home/$TUNNEL_USER"
if [ ! -d "$TUNNEL_HOME" ]; then
  TUNNEL_HOME="$(getent passwd "$TUNNEL_USER" | cut -d: -f6)"
  [ -n "$TUNNEL_HOME" ] && [ "$TUNNEL_HOME" != "/" ] || TUNNEL_HOME="/var/lib/$TUNNEL_USER"
  mkdir -p "$TUNNEL_HOME/.ssh"
else
  mkdir -p "$TUNNEL_HOME/.ssh"
fi
chown -R "$TUNNEL_USER":"$TUNNEL_USER" "$TUNNEL_HOME/.ssh" 2>/dev/null || true
chmod 700 "$TUNNEL_HOME/.ssh"

# ---------- 3. host keypair ----------
say "3. host keypair in '$KEY_DIR'"
TAG="sshtunnel-selfheal@$TUNNEL_USER"
mkdir -p "$KEY_DIR"
if [ -f "$KEY_DIR/id_ed25519" ]; then
  say "   keypair exists -- reusing (id_ed25519, $(stat -c%s "$KEY_DIR/id_ed25519" 2>/dev/null || echo '?') bytes)"
else
  ssh-keygen -q -t ed25519 -N '' -C "$TAG" -f "$KEY_DIR/id_ed25519"
  say "   keypair generated"
fi
chmod 600 "$KEY_DIR/id_ed25519"; chmod 644 "$KEY_DIR/id_ed25519.pub"
PUB="$(cat "$KEY_DIR/id_ed25519.pub")"

# ---------- 4. authorized_keys (tagged, idempotent) ----------
say "4. authorized_keys line for $TUNNEL_USER"
AK="$TUNNEL_HOME/.ssh/authorized_keys"
if [ -f "$AK" ] && grep -qF "$TAG" "$AK" 2>/dev/null; then
  say "   line already present (tag $TAG) -- skipping"
else
  [ -f "$AK" ] && cp -a "$AK" "$AK.bak-$(date +%Y%m%d-%H%M%S)" 2>/dev/null || true
  # restrict: the key can ONLY open the one reverse forward on 127.0.0.1:TUNNEL_PORT.
  # No shell, no PTY, no other forwarding, no agent, no exec.  `restrict` disables
  # all forwarding; `port-forwarding` re-enables it (narrowed by permitlisten to
  # exactly 127.0.0.1:TUNNEL_PORT), matching the project's existing tunnel keys.
  # $PUB already ends with the "$TAG" comment from ssh-keygen -C.
  printf 'restrict,command="/bin/true",port-forwarding,no-pty,permitlisten="127.0.0.1:%s" %s\n' "$TUNNEL_PORT" "$PUB" >> "$AK"
  chmod 600 "$AK"
  say "   line appended (revoke = delete the '$TAG' line)"
fi
chown "$TUNNEL_USER":"$TUNNEL_USER" "$AK" 2>/dev/null || true

# ---------- print HOST-side steps ----------
cat <<EOF

[deploy] RELAY READY.  Now on the HOST (the NAT'd machine behind this relay):
------------------------------------------------------------------
1. Copy the private key to the host:
     scp -P $RELAY_PORT root@$RELAY_HOST:$KEY_DIR/id_ed25519 $HOME/.ssh/sshtunnel-selfheal.id
   (or copy it out via any means you trust)

2. Put sshtunnel-selfheal.sh on the host and edit the config block at the top:
     KEY=$HOME/.ssh/sshtunnel-selfheal.id
     TARGET=$TUNNEL_USER@$RELAY_HOST
     PORT=$TUNNEL_PORT
     PIDFILE=\$HOME/.cache/sshtunnel-selfheal.pid
     LOGFILE=\$HOME/.cache/sshtunnel-selfheal.log

3. Start it (and it self-heals from then on):
     chmod +x sshtunnel-selfheal.sh
     ./sshtunnel-selfheal.sh start
     ./sshtunnel-selfheal.sh status

4. Verify from the relay:
     ssh -p $TUNNEL_PORT $TUNNEL_USER@127.0.0.1 'echo tunnel-up'
     (if sshd on the host listens on :22, this reaches the host's sshd)
------------------------------------------------------------------
[deploy] Done.  Re-run this script any time: it is idempotent.
EOF
