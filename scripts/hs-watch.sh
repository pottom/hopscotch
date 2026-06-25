#!/usr/bin/env bash
# hs-watch.sh — hopscotch live monitoring + reconnect test helper
#
# Usage:
#   ./scripts/hs-watch.sh                      # live status loop (Ctrl+C to stop)
#   ./scripts/hs-watch.sh test-pkill           # pkill -9 → measure recovery
#   ./scripts/hs-watch.sh test-uplink          # simulate network loss → restore
#   ./scripts/hs-watch.sh test-connect-timeout # block ping_host during connecting → 30s timeout
#   ./scripts/hs-watch.sh test-keepalive       # block ping_host after connected → keepalive failure

set -euo pipefail

ADMIN="http://127.0.0.1:8889"
UPLINK_HOST="1.1.1.1"      # target of netcheck.HasUplink()
PING_HOST="10.215.0.90"    # VPN ping_host (no port)

hs_status() {
  curl -sf "$ADMIN/status" 2>/dev/null || echo "{}"
}

vpn_field() {
  local name="$1" field="$2"
  hs_status | python3 -c "
import json,sys
d=json.load(sys.stdin)
v=d.get('vpns',{}).get('$name',{})
print(v.get('$field','?'))
" 2>/dev/null
}

# ── route blackhole helpers ────────────────────────────────────────────────────

blackhole_add() {
  local host="$1"
  sudo route add "$host" 127.0.0.1 > /dev/null 2>&1 || true
}

blackhole_del() {
  local host="$1"
  sudo route delete "$host" > /dev/null 2>&1 || true
}

# ── status display ─────────────────────────────────────────────────────────────

print_status() {
  local json
  json=$(hs_status)

  python3 - "$json" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
ts   = __import__('datetime').datetime.now().strftime('%H:%M:%S')
vpns    = data.get('vpns', {})
tunnels = data.get('tunnels', {})

for name, v in vpns.items():
    state = v.get('state', '?')
    rc    = v.get('reconnects', 0)
    upt   = int(v.get('uptime_seconds', 0))
    err   = v.get('last_error', '')
    sym   = '✓' if state == 'connected' else ('○' if state == 'disconnected' else '●')
    detail = f"uptime={upt}s RC={rc}"
    if err: detail += f" err='{err[:60]}'"
    print(f"[{ts}]  VPN {name}: {sym}{state}  {detail}")

for name, t in sorted(tunnels.items()):
    st  = t.get('status', '?')
    rc  = t.get('reconnect_count', 0)
    upt = int(t.get('uptime_seconds', 0))
    err = t.get('last_error', '')
    sym = '✓' if st == 'connected' else ('◌' if st == 'pending' else '●')
    detail = f"uptime={upt}s RC={rc}"
    if err and st != 'connected': detail += f" err='{err[:50]}'"
    print(f"[{ts}]  TUN {name}: {sym}{st}  {detail}")
print(f"[{ts}]  uplink={data.get('uplink','?')}")
PY
}

live_loop() {
  echo "=== hopscotch live monitor — Ctrl+C to stop ==="
  while true; do
    print_status
    echo "---"
    sleep 2
  done
}

# ── polling helper used by all tests ──────────────────────────────────────────

# wait_for_vpn_state <vpn_name> <target_state> <timeout_s>
wait_for_vpn_state() {
  local name="$1" target="$2" timeout="$3"
  local deadline=$(($(date +%s) + timeout))
  while true; do
    local state; state=$(vpn_field "$name" state)
    [ "$state" = "$target" ] && return 0
    if [ "$(date +%s)" -gt "$deadline" ]; then
      echo "  TIMEOUT: VPN '$name' did not reach '$target' within ${timeout}s (last: $state)" >&2
      return 1
    fi
    sleep 1
  done
}

# poll_vpn_recovery <vpn_name> <t0> <max_seconds>
# Prints one line per second, exits 0 when stable connected, 1 on timeout.
# Echoes: t_disconnected, t_reconnected as shell vars (via temp file).
poll_recovery() {
  local name="$1" t0="$2" max="$3"
  local t_dis=0 t_rec=0

  while true; do
    sleep 1
    local now elapsed json state rc uptime err
    now=$(date +%s)
    elapsed=$((now - t0))
    json=$(hs_status)

    state=$(echo  "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$name',{}).get('state','?'))" 2>/dev/null)
    rc=$(echo     "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$name',{}).get('reconnects',0))" 2>/dev/null)
    uptime=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(int(d.get('vpns',{}).get('$name',{}).get('uptime_seconds',0)))" 2>/dev/null)
    err=$(echo    "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$name',{}).get('last_error',''))" 2>/dev/null)
    uplink=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('uplink','?'))" 2>/dev/null)

    [ "$t_dis" -eq 0 ] && [ "$state" != "connected" ] && t_dis=$elapsed
    [ "$t_rec" -eq 0 ] && [ "$state" = "connected" ] && [ "$uptime" -gt 2 ] && t_rec=$elapsed

    local sym='●'; [ "$state" = "connected" ] && sym='✓'; [ "$state" = "disconnected" ] && sym='○'
    printf "  t+%3ds  %s%-12s  RC=%s uptime=%ss uplink=%s%s\n" \
      "$elapsed" "$sym" "$state" "$rc" "$uptime" "$uplink" \
      "$( [ -n "$err" ] && echo "  err='${err:0:50}'" || echo "" )"

    if [ "$t_rec" -gt 0 ] && [ "$uptime" -gt 5 ]; then
      echo "$t_dis $t_rec $rc" > /tmp/hs_poll_result
      return 0
    fi

    if [ "$elapsed" -gt "$max" ]; then
      echo "$t_dis $t_rec $rc" > /tmp/hs_poll_result
      echo "  TIMEOUT after ${max}s" >&2
      return 1
    fi
  done
}

print_result() {
  local label="$1" vpn_name="$2" pre_rc="$3"
  local t_dis t_rec rc
  read -r t_dis t_rec rc < /tmp/hs_poll_result 2>/dev/null || { t_dis=0; t_rec=0; rc=0; }

  echo ""
  echo "=== RESULT: $label ==="
  printf "  Detected loss     : t+%ss\n" "$t_dis"
  printf "  Reconnected       : t+%ss\n" "$t_rec"
  echo "  Final RC          : $rc (was $pre_rc)"

  echo ""
  echo "  Orphaned openconnect check..."
  if sudo pgrep -P 1 -x openconnect > /dev/null 2>&1; then
    echo "  WARNING: orphaned processes found!"
    sudo pgrep -la -P 1 -x openconnect 2>/dev/null || true
  else
    echo "  OK: none"
  fi

  echo ""
  echo "  Tunnel states:"
  hs_status | python3 -c "
import json, sys
d = json.load(sys.stdin)
for name, t in sorted(d.get('tunnels', {}).items()):
    st  = t.get('status', '?')
    rc  = t.get('reconnect_count', 0)
    upt = int(t.get('uptime_seconds', 0))
    sym = '✓' if st == 'connected' else ('◌' if st == 'pending' else '●')
    print(f'    {sym} {name}: {st}  RC={rc} uptime={upt}s')
" 2>/dev/null
}

# ── TEST: pkill -9 ─────────────────────────────────────────────────────────────

test_pkill() {
  local vpn_name="${1:-4ig-vpn}"
  echo "=== TEST: pkill -9 openconnect — VPN: $vpn_name ==="

  echo "Waiting for VPN to be connected..."
  wait_for_vpn_state "$vpn_name" connected 90 || exit 1
  local pre_rc; pre_rc=$(vpn_field "$vpn_name" reconnects)
  echo "  Ready. RC=$pre_rc"

  local t0; t0=$(date +%s)
  echo ""
  echo "  [$(date '+%H:%M:%S')] sudo pkill -9 openconnect"
  sudo pkill -9 openconnect 2>/dev/null || true

  echo ""
  echo "  Polling recovery..."
  poll_recovery "$vpn_name" "$t0" 120
  print_result "pkill -9" "$vpn_name" "$pre_rc"
}

# ── TEST: network uplink loss ──────────────────────────────────────────────────

test_uplink() {
  local vpn_name="${1:-4ig-vpn}"
  echo "=== TEST: network uplink loss — VPN: $vpn_name ==="

  echo "Waiting for VPN to be connected..."
  wait_for_vpn_state "$vpn_name" connected 90 || exit 1
  local pre_rc; pre_rc=$(vpn_field "$vpn_name" reconnects)
  echo "  Ready. RC=$pre_rc"

  # Block uplink check host
  local t0; t0=$(date +%s)
  echo ""
  echo "  [$(date '+%H:%M:%S')] Blackholing $UPLINK_HOST (simulating network loss)"
  blackhole_add "$UPLINK_HOST"
  trap "blackhole_del '$UPLINK_HOST'" EXIT

  # Keep blackhole for max 8 seconds (enough for hopscotch's 2s poll to fire twice)
  local t_loss=0
  local blackhole_deadline=$(($(date +%s) + 8))
  echo "  Watching for hopscotch to detect loss (max 8s)..."
  while true; do
    local state uplink elapsed
    elapsed=$(( $(date +%s) - t0 ))
    state=$(vpn_field "$vpn_name" state)
    uplink=$(hs_status | python3 -c "import json,sys; print(json.load(sys.stdin).get('uplink','?'))" 2>/dev/null)
    local sym='●'; [ "$state" = "connected" ] && sym='✓'; [ "$state" = "disconnected" ] && sym='○'
    printf "  t+%3ds  %s%-12s  uplink=%s\n" "$elapsed" "$sym" "$state" "$uplink"
    # Detected OR 8s elapsed — remove blackhole either way
    if [ "$state" != "connected" ] || [ "$(date +%s)" -ge "$blackhole_deadline" ]; then
      t_loss=$elapsed
      break
    fi
    sleep 1
  done

  echo ""
  echo "  [$(date '+%H:%M:%S')] Restoring uplink (removing blackhole)"
  blackhole_del "$UPLINK_HOST"
  trap - EXIT

  echo ""
  echo "  Polling recovery..."
  poll_recovery "$vpn_name" "$t0" 120

  local t_dis t_rec rc
  read -r t_dis t_rec rc < /tmp/hs_poll_result 2>/dev/null || { t_dis=0; t_rec=0; rc=0; }

  echo ""
  echo "=== RESULT: network uplink loss ==="
  printf "  Detected loss     : t+%ss\n" "$t_dis"
  printf "  Uplink restored   : t+%ss\n" "$t_loss"
  printf "  Reconnected       : t+%ss\n" "$t_rec"
  if [ "$t_rec" -gt 0 ] && [ "$t_loss" -gt 0 ]; then
    printf "  Restore→reconnect : %ss (expected <20s, no countdown)\n" "$((t_rec - t_loss))"
  fi
  echo "  Final RC          : $rc (was $pre_rc)"

  echo ""
  echo "  Orphaned openconnect check..."
  if sudo pgrep -P 1 -x openconnect > /dev/null 2>&1; then
    echo "  WARNING: orphaned processes found!"
  else
    echo "  OK: none"
  fi

  echo ""
  echo "  Tunnel states:"
  hs_status | python3 -c "
import json, sys
d = json.load(sys.stdin)
for name, t in sorted(d.get('tunnels', {}).items()):
    st  = t.get('status', '?')
    rc  = t.get('reconnect_count', 0)
    upt = int(t.get('uptime_seconds', 0))
    sym = '✓' if st == 'connected' else ('◌' if st == 'pending' else '●')
    print(f'    {sym} {name}: {st}  RC={rc} uptime={upt}s')
" 2>/dev/null
}

# ── TEST: connect timeout (ping_host blocked during connecting) ─────────────────

test_connect_timeout() {
  local vpn_name="${1:-4ig-vpn}"
  echo "=== TEST: connect timeout (ping_host blocked) — VPN: $vpn_name ==="

  echo "Waiting for VPN to be connected..."
  wait_for_vpn_state "$vpn_name" connected 90 || exit 1
  local pre_rc; pre_rc=$(vpn_field "$vpn_name" reconnects)
  echo "  Ready. RC=$pre_rc"

  # Block ping_host BEFORE killing VPN so new connection can't confirm
  echo ""
  echo "  Blackholing $PING_HOST (ping_host) — will prevent VPN from confirming connection"
  blackhole_add "$PING_HOST"
  trap "blackhole_del '$PING_HOST'" EXIT

  local t0; t0=$(date +%s)
  echo "  [$(date '+%H:%M:%S')] sudo pkill -9 openconnect"
  sudo pkill -9 openconnect 2>/dev/null || true

  echo ""
  echo "  Polling — expecting connect timeout at ~30s then retry..."
  local saw_timeout=0
  local deadline=$((t0 + 120))
  while true; do
    sleep 1
    local now elapsed json state rc uptime err
    now=$(date +%s)
    elapsed=$((now - t0))
    json=$(hs_status)
    state=$(echo  "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('state','?'))" 2>/dev/null)
    rc=$(echo     "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('reconnects',0))" 2>/dev/null)
    uptime=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(int(d.get('vpns',{}).get('$vpn_name',{}).get('uptime_seconds',0)))" 2>/dev/null)
    err=$(echo    "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('last_error',''))" 2>/dev/null)

    local sym='●'; [ "$state" = "connected" ] && sym='✓'; [ "$state" = "disconnected" ] && sym='○'
    printf "  t+%3ds  %s%-12s  RC=%s%s\n" \
      "$elapsed" "$sym" "$state" "$rc" \
      "$( [ -n "$err" ] && echo "  err='${err:0:60}'" || echo "" )"

    # Detect timeout event
    if echo "$err" | grep -q "connect timeout" && [ "$saw_timeout" -eq 0 ]; then
      saw_timeout=$elapsed
      echo ""
      echo "  *** connect timeout fired at t+${elapsed}s — unblocking ping_host ***"
      blackhole_del "$PING_HOST"
      trap - EXIT
    fi

    # Done when reconnected after timeout
    if [ "$saw_timeout" -gt 0 ] && [ "$state" = "connected" ] && [ "$uptime" -gt 5 ]; then
      echo ""
      echo "=== RESULT: connect timeout ==="
      printf "  Timeout fired     : t+%ss (expected ~30s after kill)\n" "$saw_timeout"
      printf "  Reconnected       : t+%ss\n" "$elapsed"
      echo "  Final RC          : $rc (was $pre_rc)"

      echo ""
      echo "  Orphaned openconnect check..."
      if sudo pgrep -P 1 -x openconnect > /dev/null 2>&1; then
        echo "  WARNING: orphaned processes found!"
      else
        echo "  OK: none"
      fi
      break
    fi

    if [ "$now" -gt "$deadline" ]; then
      blackhole_del "$PING_HOST" 2>/dev/null || true
      trap - EXIT 2>/dev/null || true
      echo "  TIMEOUT after 120s" >&2
      exit 1
    fi
  done
}

# ── TEST: keepalive failure (ping_host blocked after connected) ─────────────────

test_keepalive() {
  local vpn_name="${1:-4ig-vpn}"
  echo "=== TEST: keepalive failure (ping_host blocked mid-session) — VPN: $vpn_name ==="

  echo "Waiting for VPN to be connected and stable (15s uptime)..."
  wait_for_vpn_state "$vpn_name" connected 90 || exit 1
  # Extra wait for stability
  local stable_deadline=$(($(date +%s) + 20))
  while true; do
    local upt
    upt=$(hs_status | python3 -c "import json,sys; print(int(json.load(sys.stdin).get('vpns',{}).get('$vpn_name',{}).get('uptime_seconds',0)))" 2>/dev/null)
    [ "$upt" -gt 15 ] && break
    sleep 2
  done
  local pre_rc; pre_rc=$(vpn_field "$vpn_name" reconnects)
  local pre_upt
  pre_upt=$(hs_status | python3 -c "import json,sys; print(int(json.load(sys.stdin).get('vpns',{}).get('$vpn_name',{}).get('uptime_seconds',0)))" 2>/dev/null)
  echo "  Ready. RC=$pre_rc uptime=${pre_upt}s"

  local t0; t0=$(date +%s)
  echo ""
  echo "  [$(date '+%H:%M:%S')] Blackholing $PING_HOST (simulating internal network failure)"
  blackhole_add "$PING_HOST"
  trap "blackhole_del '$PING_HOST'" EXIT

  echo ""
  echo "  Polling — expecting 3 keepalive failures then VPN restart (~9s)..."
  local saw_restart=0
  local deadline=$((t0 + 90))
  while true; do
    sleep 1
    local now elapsed json state rc uptime err
    now=$(date +%s)
    elapsed=$((now - t0))
    json=$(hs_status)
    state=$(echo  "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('state','?'))" 2>/dev/null)
    rc=$(echo     "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('reconnects',0))" 2>/dev/null)
    uptime=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(int(d.get('vpns',{}).get('$vpn_name',{}).get('uptime_seconds',0)))" 2>/dev/null)
    err=$(echo    "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('last_error',''))" 2>/dev/null)

    local sym='●'; [ "$state" = "connected" ] && sym='✓'; [ "$state" = "disconnected" ] && sym='○'
    printf "  t+%3ds  %s%-12s  RC=%s uptime=%ss%s\n" \
      "$elapsed" "$sym" "$state" "$rc" "$uptime" \
      "$( [ -n "$err" ] && echo "  err='${err:0:50}'" || echo "" )"

    # Detect VPN restart (rc increased)
    if [ "$rc" -gt "$pre_rc" ] && [ "$saw_restart" -eq 0 ]; then
      saw_restart=$elapsed
      echo ""
      echo "  *** VPN restarted at t+${elapsed}s — unblocking ping_host ***"
      blackhole_del "$PING_HOST"
      trap - EXIT
    fi

    # Done when reconnected after restart
    if [ "$saw_restart" -gt 0 ] && [ "$state" = "connected" ] && [ "$uptime" -gt 5 ]; then
      echo ""
      echo "=== RESULT: keepalive failure ==="
      printf "  Block applied     : t+0s\n"
      printf "  VPN restart       : t+%ss (expected ~9s: 3×3s polls)\n" "$saw_restart"
      printf "  Reconnected       : t+%ss\n" "$elapsed"
      echo "  Final RC          : $rc (was $pre_rc)"

      echo ""
      echo "  Orphaned openconnect check..."
      if sudo pgrep -P 1 -x openconnect > /dev/null 2>&1; then
        echo "  WARNING: orphaned processes found!"
      else
        echo "  OK: none"
      fi
      break
    fi

    if [ "$now" -gt "$deadline" ]; then
      blackhole_del "$PING_HOST" 2>/dev/null || true
      trap - EXIT 2>/dev/null || true
      echo "  TIMEOUT after 90s" >&2
      exit 1
    fi
  done
}

# ── dispatch ───────────────────────────────────────────────────────────────────

case "${1:-live}" in
  live)             live_loop ;;
  test-pkill)       test_pkill "${2:-4ig-vpn}" ;;
  test-uplink)      test_uplink "${2:-4ig-vpn}" ;;
  test-connect-timeout) test_connect_timeout "${2:-4ig-vpn}" ;;
  test-keepalive)   test_keepalive "${2:-4ig-vpn}" ;;
  *)
    echo "Usage: $0 [live|test-pkill|test-uplink|test-connect-timeout|test-keepalive] [vpn-name]"
    exit 1
    ;;
esac
