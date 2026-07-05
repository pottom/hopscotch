#!/usr/bin/env bash
# screenshots.sh — regenerate hopscotch's TUI/web-UI documentation screenshots
# from a fully synthetic, real-capture fixture. No real infrastructure, no
# real data: a throwaway local sshd stands in for jump hosts, a stub binary
# stands in for the VPN, and a local HTTP responder is what everything
# actually reaches — but hopscotch itself runs unmodified and really
# connects, really pauses, really moves bytes.
#
# Usage:
#   ./scripts/demo/screenshots.sh setup      # build fixture + start hopscotch
#   ./scripts/demo/screenshots.sh warm       # wait for connect, pause one tunnel, start traffic
#   ./scripts/demo/screenshots.sh capture    # vhs (TUI) + Playwright (web UI) -> docs/*.png
#   ./scripts/demo/screenshots.sh teardown   # kill everything, undo /etc/hosts, wipe scratch dir
#   ./scripts/demo/screenshots.sh all        # setup + warm + capture, teardown always runs after
#
# Requires: vhs (brew install vhs), python3, playwright (chromium already
# installed), a local sshd binary (/usr/sbin/sshd on macOS), sudo (one-time,
# to add/remove a clearly-marked temporary /etc/hosts block).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DOCS_DIR="$REPO_ROOT/docs"

FAKE_DOMAIN="hs.test"  # .test is IANA/RFC 2606-reserved for non-production use — short too, so it fits the TUI's Host column without wrapping
SSHD_PORT=2299
PROXY_PORT=18080
ADMIN_PORT=18089
RESPONDER_PORT=18100
VPN_PING_PORT=18101

STATE_FILE="/tmp/hopscotch-demo-state.env"  # scratch dir + PIDs, shared across subcommands

HOSTS_MARKER_BEGIN="# hopscotch-demo BEGIN"
HOSTS_MARKER_END="# hopscotch-demo END"

log() { echo "[demo] $*"; }

# ── setup ──────────────────────────────────────────────────────────────────────

setup() {
  local scratch
  scratch=$(mktemp -d "${TMPDIR:-/tmp}/hopscotch-demo.XXXXXX")
  log "scratch dir: $scratch"

  log "generating throwaway SSH keys..."
  ssh-keygen -q -t ed25519 -N "" -f "$scratch/ssh_host_ed25519_key"
  ssh-keygen -q -t ed25519 -N "" -f "$scratch/id_ed25519" -C "hopscotch-demo"
  cp "$scratch/id_ed25519.pub" "$scratch/authorized_keys"
  : > "$scratch/known_hosts"

  cat > "$scratch/sshd_config" <<EOF
Port $SSHD_PORT
ListenAddress 127.0.0.1
HostKey $scratch/ssh_host_ed25519_key
PidFile $scratch/sshd.pid
AuthorizedKeysFile $scratch/authorized_keys
PubkeyAuthentication yes
PasswordAuthentication no
PermitRootLogin no
AllowTcpForwarding yes
X11Forwarding no
PrintMotd no
UsePAM no
StrictModes no
LogLevel ERROR
EOF

  log "starting throwaway sshd on 127.0.0.1:$SSHD_PORT..."
  /usr/sbin/sshd -f "$scratch/sshd_config" -D -e > "$scratch/sshd.log" 2>&1 &
  local sshd_pid=$!
  sleep 1
  if ! kill -0 "$sshd_pid" 2>/dev/null; then
    echo "sshd failed to start — see $scratch/sshd.log" >&2
    cat "$scratch/sshd.log" >&2
    exit 1
  fi

  log "starting dummy HTTP responder on 127.0.0.1:$RESPONDER_PORT..."
  python3 "$SCRIPT_DIR/dummy-responder.py" "$RESPONDER_PORT" > "$scratch/responder.log" 2>&1 &
  local responder_pid=$!

  log "starting VPN ping-target listener on 127.0.0.1:$VPN_PING_PORT..."
  python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $VPN_PING_PORT))
s.listen(5)
while True:
    conn, _ = s.accept()
    conn.close()
" > "$scratch/vpn-ping.log" 2>&1 &
  local vpnping_pid=$!

  log "adding temporary /etc/hosts block (sudo)..."
  local hosts_block
  hosts_block="$HOSTS_MARKER_BEGIN (scripts/demo/screenshots.sh — safe to delete this block)
127.0.0.1 db-primary.$FAKE_DOMAIN
127.0.0.1 api-gateway.$FAKE_DOMAIN
127.0.0.1 edge-cache.$FAKE_DOMAIN
127.0.0.1 jump.staging.$FAKE_DOMAIN
127.0.0.1 static-assets.$FAKE_DOMAIN
$HOSTS_MARKER_END"
  if grep -q "$HOSTS_MARKER_BEGIN" /etc/hosts 2>/dev/null; then
    log "  (marker block already present, leaving it)"
  else
    printf '%s\n' "$hosts_block" | sudo tee -a /etc/hosts > /dev/null
  fi

  # Written now (before the riskier build/trust/start steps below) so a
  # failure partway through still leaves `teardown` able to find and kill
  # these processes and remove the /etc/hosts block.
  cat > "$STATE_FILE" <<EOF
SCRATCH=$scratch
SSHD_PID=$sshd_pid
RESPONDER_PID=$responder_pid
VPNPING_PID=$vpnping_pid
PROXY_PORT=$PROXY_PORT
ADMIN_PORT=$ADMIN_PORT
RESPONDER_PORT=$RESPONDER_PORT
FAKE_DOMAIN=$FAKE_DOMAIN
EOF

  cat > "$scratch/config.yaml" <<EOF
tunnels:
  - name: db-primary
    host: db-primary.$FAKE_DOMAIN
    port: $SSHD_PORT
    user: $(whoami)
    identity_file: $scratch/id_ed25519
    known_hosts_file: $scratch/known_hosts
    local_port: 15001
    requires_vpn: corp-vpn
    auto_pause_threshold: 3

  - name: api-gateway
    host: api-gateway.$FAKE_DOMAIN
    port: $SSHD_PORT
    user: $(whoami)
    identity_file: $scratch/id_ed25519
    known_hosts_file: $scratch/known_hosts
    local_port: 15002
    requires_vpn: corp-vpn
    auto_pause_threshold: 3

  - name: staging-jump
    host: jump.staging.$FAKE_DOMAIN
    port: $SSHD_PORT
    user: $(whoami)
    identity_file: $scratch/id_ed25519
    known_hosts_file: $scratch/known_hosts
    local_port: 15003
    auto_pause_threshold: 3

  - name: edge-cache
    host: edge-cache.$FAKE_DOMAIN
    port: 19999
    user: $(whoami)
    identity_file: $scratch/id_ed25519
    known_hosts_file: $scratch/known_hosts
    local_port: 15004
    auto_pause_threshold: 1

vpn:
  - name: corp-vpn
    type: openconnect
    server: vpn.$FAKE_DOMAIN
    binary: $SCRIPT_DIR/fake-openconnect.sh
    ping_host: 127.0.0.1:$VPN_PING_PORT
    password_env: HOPSCOTCH_DEMO_VPN_PASSWORD

proxy:
  port: $PROXY_PORT
  bind: 127.0.0.1
  rules:
    - pattern: db-primary.$FAKE_DOMAIN
      target: db-primary
      comment: Primary Postgres jump host
    - pattern: api-gateway.$FAKE_DOMAIN
      target: api-gateway
      comment: Internal API gateway
    - pattern: edge-cache.$FAKE_DOMAIN
      target: edge-cache
    - pattern: "10.42.0.0/16"
      target: block
      comment: Legacy test range, intentionally blocked
    - pattern: "*.staging.$FAKE_DOMAIN"
      target: staging-jump
    - pattern: "*"
      target: direct

admin:
  port: $ADMIN_PORT
  bind: 127.0.0.1
  show_public_ip: false

notifications:
  enabled: true
  on_disconnect: true
  on_reconnect: true
  on_auto_pause: true
  sound: false
EOF

  log "building hopscotch..."
  (cd "$REPO_ROOT" && ./build.sh binary > /tmp/hopscotch-demo-build.log 2>&1) || {
    echo "build failed — see /tmp/hopscotch-demo-build.log" >&2
    exit 1
  }

  log "trusting fake sshd host key into scratch known_hosts..."
  export HOPSCOTCH_DEMO_VPN_PASSWORD="unused"
  # edge-cache is deliberately unreachable (demonstrates auto-pause via a real
  # connection-refused failure) so it has no host key to trust — skip it.
  for tunnel in db-primary api-gateway staging-jump; do
    "$REPO_ROOT/dist/hopscotch" trust "$tunnel" --config "$scratch/config.yaml" --known-hosts "$scratch/known_hosts" -y
  done

  log "starting hopscotch (foreground, backgrounded, isolated config/state)..."
  HOPSCOTCH_CONTAINER=true HOPSCOTCH_DEMO_VPN_PASSWORD="unused" \
    "$REPO_ROOT/dist/hopscotch" start --foreground --config "$scratch/config.yaml" \
    > "$scratch/hopscotch.log" 2>&1 &
  local hopscotch_pid=$!
  echo "HOPSCOTCH_PID=$hopscotch_pid" >> "$STATE_FILE"

  log "setup done. state: $STATE_FILE"
}

# ── warm ───────────────────────────────────────────────────────────────────────

warm() {
  # shellcheck disable=SC1090
  source "$STATE_FILE"

  log "waiting for hopscotch admin API..."
  local tries=0
  until curl -sf "http://127.0.0.1:$ADMIN_PORT/status" > /dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -gt 30 ]; then
      echo "hopscotch admin API never came up — see $SCRATCH/hopscotch.log" >&2
      exit 1
    fi
    sleep 1
  done

  log "waiting for db-primary/api-gateway tunnels + corp-vpn to connect..."
  tries=0
  while true; do
    local ok
    ok=$(curl -sf "http://127.0.0.1:$ADMIN_PORT/status" | python3 -c "
import json,sys
d=json.load(sys.stdin)
t=d.get('tunnels',{})
v=d.get('vpns',{})
ready = (t.get('db-primary',{}).get('status')=='connected'
     and t.get('api-gateway',{}).get('status')=='connected'
     and v.get('corp-vpn',{}).get('state')=='connected')
print('yes' if ready else 'no')
")
    [ "$ok" = "yes" ] && break
    tries=$((tries + 1))
    if [ "$tries" -gt 30 ]; then
      echo "tunnels/VPN never reached connected — see $SCRATCH/hopscotch.log" >&2
      exit 1
    fi
    sleep 1
  done
  log "  connected."

  log "pausing staging-jump (manual pause demo state)..."
  curl -sf -X POST "http://127.0.0.1:$ADMIN_PORT/api/tunnels/staging-jump/pause" > /dev/null

  log "waiting for edge-cache to auto-pause (real connection-refused failures)..."
  tries=0
  while true; do
    local paused
    paused=$(curl -sf "http://127.0.0.1:$ADMIN_PORT/status" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print('yes' if d.get('tunnels',{}).get('edge-cache',{}).get('auto_paused') else 'no')
")
    [ "$paused" = "yes" ] && break
    tries=$((tries + 1))
    if [ "$tries" -gt 30 ]; then
      echo "edge-cache never auto-paused — see $SCRATCH/hopscotch.log" >&2
      exit 1
    fi
    sleep 1
  done
  log "  auto-paused."

  log "starting traffic generator (background, steady pace)..."
  (
    while true; do
      curl -s -o /dev/null -x "socks5h://127.0.0.1:$PROXY_PORT" \
        "http://db-primary.$FAKE_DOMAIN:$RESPONDER_PORT/" || true
      sleep 0.4
      curl -s -o /dev/null -x "socks5h://127.0.0.1:$PROXY_PORT" \
        "http://api-gateway.$FAKE_DOMAIN:$RESPONDER_PORT/" || true
      sleep 0.4
      curl -s -o /dev/null -x "socks5h://127.0.0.1:$PROXY_PORT" \
        "http://static-assets.$FAKE_DOMAIN:$RESPONDER_PORT/" || true
      sleep 0.6
    done
  ) &
  echo "TRAFFIC_PID=$!" >> "$STATE_FILE"

  log "letting traffic run for 15s so uptime/bytes/graphs look organic..."
  sleep 15
  log "warm done."
}

# ── capture ────────────────────────────────────────────────────────────────────

capture() {
  # shellcheck disable=SC1090
  source "$STATE_FILE"
  mkdir -p "$DOCS_DIR"

  log "capturing TUI screenshots (vhs)..."
  (cd "$REPO_ROOT" && HOPSCOTCH_CONFIG="$SCRATCH/config.yaml" vhs "$SCRIPT_DIR/capture_tui.tape")
  rm -f "$REPO_ROOT/.hopscotch-demo-tui.gif"  # vhs's Output artifact — only the mid-tape Screenshots matter

  log "capturing web UI screenshots (Playwright)..."
  NODE_PATH="$(npm root -g)" node "$SCRIPT_DIR/capture_web.js" "$ADMIN_PORT" "$DOCS_DIR"

  # docs/AGENTS.md: screenshots must stay identical in both docs/ (GitHub
  # README) and internal/admin/ui/docs/ (the served in-app copy).
  log "mirroring screenshots into internal/admin/ui/docs/..."
  mkdir -p "$REPO_ROOT/internal/admin/ui/docs"
  cp "$DOCS_DIR"/tui-*.png "$DOCS_DIR"/ui-*.png "$REPO_ROOT/internal/admin/ui/docs/"

  log "capture done."
}

# ── teardown ───────────────────────────────────────────────────────────────────

teardown() {
  [ -f "$STATE_FILE" ] || { log "no state file, nothing to tear down"; return 0; }
  # shellcheck disable=SC1090
  source "$STATE_FILE"

  log "tearing down..."
  for pid_var in TRAFFIC_PID HOPSCOTCH_PID VPNPING_PID RESPONDER_PID SSHD_PID; do
    local pid="${!pid_var:-}"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  pkill -f "fake-openconnect.sh" 2>/dev/null || true
  sleep 1

  if [ -n "${SCRATCH:-}" ]; then
    rm -rf "$SCRATCH"
  fi

  log "removing temporary /etc/hosts block (sudo)..."
  sudo sed -i.bak "/$HOSTS_MARKER_BEGIN/,/$HOSTS_MARKER_END/d" /etc/hosts
  sudo rm -f /etc/hosts.bak

  rm -f "$STATE_FILE"
  log "teardown done."
}

# ── dispatch ───────────────────────────────────────────────────────────────────

case "${1:-all}" in
  setup)    setup ;;
  warm)     warm ;;
  capture)  capture ;;
  teardown) teardown ;;
  all)
    trap teardown EXIT
    setup
    warm
    capture
    ;;
  *)
    echo "Usage: $0 [setup|warm|capture|teardown|all]"
    exit 1
    ;;
esac
