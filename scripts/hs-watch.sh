#!/usr/bin/env bash
# hs-watch.sh — hopscotch live monitoring + reconnect test helper
#
# Usage:
#   ./scripts/hs-watch.sh              # live status loop (Ctrl+C to stop)
#   ./scripts/hs-watch.sh test-pkill   # run pkill -9 and measure recovery time
#   ./scripts/hs-watch.sh test-wifi    # instructions for wifi-drop test

set -euo pipefail

ADMIN="http://127.0.0.1:8889"

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

print_status() {
  local json
  json=$(hs_status)
  local ts; ts=$(date '+%H:%M:%S')

  python3 - "$json" <<'PY'
import json, sys

data = json.loads(sys.argv[1])
ts   = __import__('datetime').datetime.now().strftime('%H:%M:%S')

# VPNs
vpns = data.get('vpns', {})
tunnels = data.get('tunnels', {})

vpn_parts = []
for name, v in vpns.items():
    state = v.get('state', '?')
    rc    = v.get('reconnects', 0)
    upt   = int(v.get('uptime_seconds', 0))
    nxt   = v.get('next_reconnect_in', '')
    err   = v.get('last_error', '')
    sym   = '✓' if state == 'connected' else ('○' if state == 'disconnected' else '●')
    detail = f"uptime={upt}s RC={rc}"
    if nxt:
        detail += f" next={nxt}"
    if err:
        detail += f" err='{err}'"
    vpn_parts.append(f"  VPN {name}: {sym}{state}  {detail}")

tun_parts = []
for name, t in sorted(tunnels.items()):
    st  = t.get('status', '?')
    rc  = t.get('reconnect_count', 0)
    upt = int(t.get('uptime_seconds', 0))
    err = t.get('last_error', '')
    sym = '✓' if st == 'connected' else ('◌' if st == 'pending' else '●')
    detail = f"uptime={upt}s RC={rc}"
    if err and st != 'connected':
        short_err = err[:60]
        detail += f" err='{short_err}'"
    tun_parts.append(f"  TUN {name}: {sym}{st}  {detail}")

print(f"[{ts}]  uplink={data.get('uplink','?')}")
for p in vpn_parts:
    print(p)
for p in tun_parts:
    print(p)
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

test_pkill() {
  local vpn_name="${1:-4ig-vpn}"

  echo "=== hs-watch: pkill -9 reconnect test (VPN: $vpn_name) ==="

  # Confirm VPN is connected before test
  echo "Waiting for VPN '$vpn_name' to be connected..."
  local deadline=$(($(date +%s) + 60))
  while true; do
    state=$(vpn_field "$vpn_name" state)
    if [ "$state" = "connected" ]; then
      echo "  VPN connected. Starting test."
      break
    fi
    if [ "$(date +%s)" -gt "$deadline" ]; then
      echo "  ERROR: VPN not connected within 60s, aborting." >&2
      exit 1
    fi
    echo "  current state: $state — waiting..."
    sleep 2
  done

  # Snapshot pre-kill RC
  local pre_rc
  pre_rc=$(vpn_field "$vpn_name" reconnects)
  echo "  Pre-kill reconnects: $pre_rc"

  # Kill
  local t0; t0=$(date +%s)
  echo ""
  echo "  [$(date '+%H:%M:%S')] sudo pkill -9 openconnect"
  sudo pkill -9 openconnect 2>/dev/null || true

  # Poll until reconnected
  echo ""
  echo "  Polling recovery..."
  local t_disconnected=0 t_reconnected=0
  while true; do
    sleep 1
    local now elapsed json state rc uptime err nxt
    now=$(date +%s)
    elapsed=$((now - t0))
    json=$(hs_status)

    state=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('state','?'))" 2>/dev/null)
    rc=$(echo    "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('reconnects',0))" 2>/dev/null)
    uptime=$(echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(int(d.get('vpns',{}).get('$vpn_name',{}).get('uptime_seconds',0)))" 2>/dev/null)
    err=$(echo   "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('last_error',''))" 2>/dev/null)
    nxt=$(echo   "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('vpns',{}).get('$vpn_name',{}).get('next_reconnect_in',''))" 2>/dev/null)

    # Detect transitions
    if [ "$t_disconnected" -eq 0 ] && [ "$state" != "connected" ]; then
      t_disconnected=$elapsed
    fi
    if [ "$t_reconnected" -eq 0 ] && [ "$state" = "connected" ] && [ "$uptime" -gt 2 ]; then
      t_reconnected=$elapsed
    fi

    local sym='●'; [ "$state" = "connected" ] && sym='✓'; [ "$state" = "disconnected" ] && sym='○'
    local detail="RC=$rc uptime=${uptime}s"
    [ -n "$nxt" ] && detail="$detail next=$nxt"
    [ -n "$err" ] && detail="$detail err='${err:0:50}'"
    printf "  t+%3ds  %s%-12s  %s\n" "$elapsed" "$sym" "$state" "$detail"

    # Done when reconnected and stable
    if [ "$t_reconnected" -gt 0 ] && [ "$uptime" -gt 5 ]; then
      echo ""
      echo "=== RESULT ==="
      echo "  Kill → disconnected detected : ${t_disconnected}s"
      echo "  Kill → reconnected           : ${t_reconnected}s"
      echo "  Backoff used                 : $((t_reconnected - t_disconnected - 10))s (approx, minus ~10s connect)"
      echo "  Final RC                     : $rc (was $pre_rc)"

      # Check orphaned procs
      echo ""
      echo "  Checking for orphaned openconnect (PPID=1)..."
      if sudo pgrep -P 1 -x openconnect > /dev/null 2>&1; then
        echo "  WARNING: orphaned openconnect processes found!"
        sudo pgrep -la -P 1 -x openconnect 2>/dev/null || true
      else
        echo "  OK: no orphaned openconnect processes"
      fi

      # Check tunnel recovery
      echo ""
      echo "  Tunnel states after VPN recovery:"
      echo "$json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for name, t in sorted(d.get('tunnels', {}).items()):
    st  = t.get('status', '?')
    rc  = t.get('reconnect_count', 0)
    upt = int(t.get('uptime_seconds', 0))
    sym = '✓' if st == 'connected' else ('◌' if st == 'pending' else '●')
    print(f'    {sym} {name}: {st}  RC={rc} uptime={upt}s')
" 2>/dev/null
      break
    fi

    # Safety timeout
    if [ "$elapsed" -gt 120 ]; then
      echo "  ERROR: VPN did not reconnect within 120s" >&2
      exit 1
    fi
  done
}

case "${1:-live}" in
  live)
    live_loop
    ;;
  test-pkill)
    test_pkill "${2:-4ig-vpn}"
    ;;
  *)
    echo "Usage: $0 [live|test-pkill [vpn-name]]"
    exit 1
    ;;
esac
