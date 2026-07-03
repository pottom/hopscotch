#!/usr/bin/env bash
# fake-openconnect.sh — stub VPN binary for the docs screenshot fixture.
#
# Stands in for the real `openconnect` binary in scripts/demo/screenshots.sh's
# synthetic config: hopscotch's VPN manager spawns this, and detects
# "connected" purely via a TCP ping_host probe (see internal/vpn/openconnect.go
# pollPingHost) — this script just needs to stay alive and exit cleanly on
# SIGTERM, it never has to speak the real VPN protocol.
set -euo pipefail

trap 'exit 0' TERM INT

echo "fake-openconnect: pretending to connect (demo fixture, not a real VPN)"
while true; do
  sleep 3600 &
  wait $!
done
