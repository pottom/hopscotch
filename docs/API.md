# REST API Reference

hopscotch's admin server exposes a small HTTP API on `admin.bind:admin.port`
(default `127.0.0.1:9090`). It backs both the TUI and the web UI — anything
either UI can do, this API can do too, since both drive the daemon exclusively
through these endpoints (no separate in-process control path).

All request/response bodies are JSON unless noted otherwise.

## Authentication

Auth is all-or-nothing, controlled by `admin.username`/`admin.password` in
`config.yaml`:

- **Unset** (default): no authentication, every endpoint below is open.
- **Set**: every endpoint requires a valid session cookie, obtained via
  `POST /api/login`. Requests without one get `401 Unauthorized`; a browser
  navigating to `/` without a session is redirected to `/login`.

```
POST /api/login
Content-Type: application/x-www-form-urlencoded

username=alice&password=hunter2
```

On success: sets an `hs_session` cookie (`HttpOnly`, `SameSite=Strict`) and
redirects (`303`) to `/`. On failure: redirects to `/login?error=1`. The
session token is random per process start and is not persisted — restarting
the daemon invalidates all sessions.

```
POST /api/logout
```

Clears the cookie and redirects to `/login`.

All examples below assume no auth, or that `-b cookies.txt` / an equivalent
cookie jar is already carrying a valid session.

---

## GET /health

Liveness/readiness check. Returns `200` when every tunnel is `connected` or
manually `paused`, `503` otherwise (so it also works as a naive Kubernetes
readiness probe — a VPN retry loop or a bad jump host will fail it).

```json
{
  "status": "healthy",
  "version": "v0.9.0",
  "uptime": "2h13m5s",
  "tunnels": {
    "prod-jump": "connected",
    "staging-jump": "connected"
  }
}
```

## GET /metrics

Prometheus text-format metrics (`text/plain; version=0.0.4`). See the
[Prometheus metrics](../README.md#prometheus-metrics) table in the README for
the full metric list — `hopscotch_tunnel_status`,
`hopscotch_tunnel_bytes_in_total`, `hopscotch_vpn_status`, etc.

## GET /status

The full live snapshot — this is what both UIs poll once per second to render
everything (tunnels, VPNs, routes, notification settings, header metadata).

```json
{
  "status": "healthy",
  "version": "v0.9.0",
  "latest_version": "",
  "uptime": "2h13m5s",
  "pid": 14975,
  "proxy_port": 8080,
  "proxy_bind": "0.0.0.0",
  "proxy_auth_enabled": false,
  "admin_port": 9090,
  "admin_bind": "127.0.0.1",
  "admin_auth_enabled": false,
  "uplink": true,
  "uplink_iface": "en0",
  "uplink_ip": "192.168.1.42",
  "internet": true,
  "public_ip": "203.0.113.7",
  "tunnels": {
    "prod-jump": {
      "status": "connected",
      "host": "bastion.corp.com:22",
      "local_port": 1080,
      "reconnect_count": 0,
      "uptime_seconds": 7985.2,
      "requires_vpn": "corp-vpn",
      "keepalive_failures": 0,
      "last_error": "",
      "bytes_in": 1048576,
      "bytes_out": 65536,
      "consecutive_failures": 0,
      "auto_pause_threshold": 5,
      "auto_paused": false
    }
  },
  "vpns": {
    "corp-vpn": {
      "state": "connected",
      "host": "vpn.corp.com",
      "reconnects": 0,
      "uptime_seconds": 8000.1,
      "tun_iface": "utun4",
      "reconnect_in": null,
      "last_error": "",
      "consecutive_failures": 0,
      "auto_pause_threshold": 0,
      "auto_paused": false
    }
  },
  "routes": [
    { "pattern": "*.prod.internal", "target": "prod-jump" },
    { "pattern": "*", "target": "direct" }
  ],
  "notifications": {
    "enabled": true,
    "on_disconnect": true,
    "on_reconnect": true,
    "on_auto_pause": true,
    "sound": false
  }
}
```

Notes:
- `tunnels`/`vpns` are keyed by name (as configured), not arrays.
- `reconnect_in` (VPN) is `null` unless currently in a reconnect backoff.
- `vpns` is omitted entirely when no VPNs are configured.
- `last_error` is empty while connected; see the root-cause propagation rule
  in [`DESIGN.md`](DESIGN.md) (a tunnel waiting on a VPN surfaces the VPN's
  own error, not "waiting for VPN: X").

## GET /readme

Returns the repo's `README.md` verbatim (`Content-Type: text/markdown`) — this
is what powers the web UI's Docs tab.

## GET /traffic/stream

Server-Sent Events stream, one `data:` message per second, with per-second
traffic deltas (bytes/s) layered on top of the cumulative totals already in
`/status`. Used for the live bps graphs — `/status` alone is only polled once
a second and doesn't carry rate data.

```
GET /traffic/stream
Accept: text/event-stream
```

```
data: {"tunnels":{"prod-jump":{"bps_in":4096,"bps_out":512,"bytes_in":1052672,"bytes_out":66048,"active":2}},"vpns":{"corp-vpn":{"state":"connected"}},"direct":{"bps_in":0,"bps_out":0,"bytes_in":0,"bytes_out":0,"active":0}}

```

Each tunnel entry may also carry `"reconnect_in": <seconds>` while backing
off. The connection stays open until the client disconnects; no polling
needed.

## GET /logs/stream

Server-Sent Events stream of structured log lines. On connect, sends the
recent backlog first (in-memory ring buffer), then live lines as they're
emitted. A `: ping` comment line is sent every 15s to keep idle proxies/load
balancers from closing the connection.

```
GET /logs/stream?level=INFO
Accept: text/event-stream
```

| Query param | Values | Default |
|---|---|---|
| `level` | `DEBUG` \| `INFO` \| `WARN` \| `ERROR` | `DEBUG` (send everything; let the client filter) |

Each `data:` line is one already-formatted log line (the same text the daemon
writes to its own log output) — there's no structured JSON envelope here,
unlike `/traffic/stream`. Source (`tunnel`/`vpn`/`proxy`/`system`) and text
filtering both happen client-side.

---

## PUT /api/rules

Replaces the entire routing rule set, atomically. Persists to `config.yaml`
and applies to the live SOCKS5 router immediately — no restart, no reload.

```json
PUT /api/rules
Content-Type: application/json

{
  "rules": [
    { "pattern": "*.prod.internal", "target": "prod-jump" },
    { "pattern": "10.0.0.0/8", "target": "prod-jump", "comment": "internal net" },
    { "pattern": "*", "target": "direct" }
  ]
}
```

`target` is a tunnel name, `"direct"`, or `"block"`. Every rule needs a
non-empty `pattern` and `target`; `pattern` must be valid per the grammar
below (checked with the same validator as `GET /api/validate-pattern`).
`comment` is optional and freeform.

Responses:
- `200` — `{"status":"ok"}`
- `400` — bad JSON, missing `pattern`/`target`, or an invalid pattern (body is
  a plain-text error message, not JSON)
- `500` — failed to persist to `config.yaml`

## GET /api/validate-pattern

Validates a single pattern without saving anything — powers the Rules tab's
live-as-you-type feedback.

```
GET /api/validate-pattern?p=*.example.com
```

```json
{ "valid": true }
```

```json
{ "valid": false, "error": "..." }
```

Pattern grammar: exact host, `*` wildcard, `*.suffix`, `prefix.*`, or CIDR
(e.g. `10.0.0.0/8`).

## PUT /api/notifications

Replaces the native desktop-notification settings — applies live to the
running notifier immediately (no restart) and persists to `config.yaml`. This
is what the Settings tab in both UIs calls on every toggle.

```json
PUT /api/notifications
Content-Type: application/json

{
  "enabled": true,
  "on_disconnect": true,
  "on_reconnect": true,
  "on_auto_pause": true,
  "sound": false
}
```

Responses:
- `200` — echoes back the applied settings as JSON
- `400` — invalid JSON
- `404` — notifications subsystem not available (shouldn't happen in normal operation)
- `500` — failed to persist to `config.yaml`

There's no partial-update form — always send all five fields.

## POST /api/tunnels/{name}/reconnect

Forces an immediate reconnect of the named tunnel, skipping any backoff
delay. Marks the resulting disconnect/reconnect blip as intentional so it
doesn't trigger a false desktop notification (see
[`internal/notify/AGENTS.md`](../internal/notify/AGENTS.md)).

- `204 No Content` on success
- `404` — `tunnel not found`

## POST /api/tunnels/{name}/pause

Manually pauses the named tunnel — stops retrying until resumed. Persisted:
survives a daemon restart (`internal/state.PausedTracker`).

- `204 No Content` on success
- `404` — `tunnel not found`

## POST /api/tunnels/{name}/resume

Resumes a manually-paused tunnel (immediate reconnect attempt). Also suppresses
the resulting connect blip from firing a false notification.

- `204 No Content` on success
- `404` — `tunnel not found`

## POST /api/vpns/{name}/reconnect

## POST /api/vpns/{name}/pause

## POST /api/vpns/{name}/resume

Same semantics as the tunnel endpoints above, for a configured VPN.

- `204 No Content` on success
- `404` — `vpn not found`, or `no vpns configured` if the daemon has none at all

---

## Auto-pause is not an API action

`auto_pause_threshold` (per-tunnel/VPN config) causes the daemon to
self-pause after N consecutive failed connection attempts — this happens
internally in `internal/tunnel`/`internal/vpn`, not via any of the endpoints
above, and is **not** persisted (it's a computed reaction, re-evaluated fresh
on every restart, not a stored user intent). `GET /status`'s `auto_paused`
field is how you tell an auto-pause apart from a manual one when
`status`/`state == "paused"`.

`auto_resume_after` (seconds, per-tunnel/VPN config, 0 disables) is the
sibling of `auto_pause_threshold`: once an *auto*-pause fires, the daemon
retries on its own after this many seconds instead of waiting for
`POST /.../resume`. It's config-only and does **not** appear anywhere in the
`GET /status` JSON — there is currently no field exposing a countdown or
deadline for when the next automatic retry will happen; the only externally
visible signal is `status`/`state` eventually leaving `"paused"` on its own.
A *manual* pause (`POST /.../pause`) is never eligible for auto-resume,
regardless of this setting — only the daemon's own auto-pause is.
