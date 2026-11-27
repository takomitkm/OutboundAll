#!/bin/bash
# sshtunnel-selfheal -- reverse-anchor a NAT'd host's sshd onto a reachable relay
# while it runs, on the relay:  127.0.0.1:<LOCAL_PORT>  reaches the host's sshd
#   e.g.   ssh -p <LOCAL_PORT> <relay-user>@127.0.0.1
#
# The self-healing part: a detached daemon loop restarts the ssh -R process
# whenever it exits, so the tunnel survives ssh drops / relay restarts without
# manual intervention (the reverse-tunnel equivalent of a supervisor).
#
# usage:
#   sshtunnel-selfheal            foreground (tied to your ssh session, Ctrl+C stops)
#   sshtunnel-selfheal start      DETACHED: survives ssh logout/drop, auto-reconnect
#   sshtunnel-selfheal stop       kill the detached one
#   sshtunnel-selfheal status     is the detached one alive?
#
# --- configuration (edit for your topology) ---
# This script runs ON THE HOST (the NAT'd machine). It connects OUT to the relay.
KEY=/path/to/RELAY_KEY                # private key the HOST uses to authenticate to the relay
TARGET=RELAY_USER@RELAY_HOST          # relay user@host (what the host connects to)
PORT=13390                            # loopback port opened ON the relay (the dial-back entry)
PIDFILE=$HOME/.cache/sshtunnel-selfheal.pid
LOGFILE=$HOME/.cache/sshtunnel-selfheal.log
# ------------------------------------------------
OPTS=(-N -R "$PORT":127.0.0.1:22
      -i "$KEY"
      -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10
      -o ServerAliveInterval=30 -o ServerAliveCountMax=3
      -o ExitOnForwardFailure=yes)
REAL=$(readlink -e "$0")

is_running() {
  [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null
}

case "${1:-run}" in
  run)
    [ -t 0 ] && echo "[sshtunnel-selfheal] host :22 -> relay 127.0.0.1:$PORT (Ctrl+C to stop)"
    exec ssh "${OPTS[@]}" "$TARGET"
    ;;
  start)
    if is_running; then
      echo "[sshtunnel-selfheal] already running (pid $(cat "$PIDFILE"), log: $LOGFILE)"
      exit 0
    fi
    mkdir -p "$(dirname "$PIDFILE")"
    # keep last 200KB of log only
    if [ -f "$LOGFILE" ] && [ "$(stat -c%s "$LOGFILE" 2>/dev/null || echo 0)" -gt 204800 ]; then : > "$LOGFILE"; fi
    # setsid: own session, no controlling tty -> no SIGHUP on ssh logout
    # nohup:   belt and braces   /   </dev/null: stdin free   /   &: returns to you
    setsid nohup "$REAL" daemon >>"$LOGFILE" 2>&1 </dev/null &
    sleep 0.3
    if is_running; then
      echo "[sshtunnel-selfheal] started detached (pid $(cat "$PIDFILE"), log: $LOGFILE)"
    else
      echo "[sshtunnel-selfheal] start FAILED, log tail:"; tail -3 "$LOGFILE"; exit 1
    fi
    ;;
  daemon)
    echo $$ > "$PIDFILE"
    echo "[sshtunnel-selfheal] $(date '+%F %T') daemon up (pid $$)"
    child=
    trap '[ -n "$child" ] && kill "$child" 2>/dev/null; rm -f "$PIDFILE"; exit 0' TERM INT
    while :; do
      ssh "${OPTS[@]}" "$TARGET" &
      child=$!
      wait "$child"; rc=$?
      echo "[sshtunnel-selfheal] $(date '+%F %T') ssh exited rc=$rc, reconnecting in 5s"
      sleep 5
    done
    ;;
  stop)
    if [ -f "$PIDFILE" ]; then
      if kill "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "[sshtunnel-selfheal] stopped (pid $(cat "$PIDFILE"))"
      else
        echo "[sshtunnel-selfheal] pidfile stale, cleaned"
      fi
      rm -f "$PIDFILE"
    else
      echo "[sshtunnel-selfheal] not running"
    fi
    pkill -f 'sshtunnel-selfheal daemon' 2>/dev/null
    ;;
  status)
    if is_running; then
      echo "[sshtunnel-selfheal] RUNNING (pid $(cat "$PIDFILE"), log: $LOGFILE)"
    else
      echo "[sshtunnel-selfheal] not running"
    fi
    ;;
  *)
    echo "usage: $0 [start|stop|status]  (bare = foreground run)"
    exit 1
    ;;
esac
