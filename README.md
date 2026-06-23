# hopscotch

SSH tunnel manager with automatic reconnect and a built-in SOCKS5 proxy router that routes connections by URL pattern.

```
$ hopscotch start
hopscotch started (PID 12345)

$ hopscotch status
hopscotch dev  ✓ healthy  PID 12345  up 3m

TUNNEL                   PORT   STATUS         UPTIME   RECONNECTS
─────────────────────────────────────────────────────────────────
go-a-preprod-jump        1082   ✓ connected    3m12s    0
go-b-preprod-jump        1083   ✓ connected    3m11s    0
```

## Features

- **Automatic reconnect** with exponential backoff — tunnels come back on their own after network interruptions
- **SOCKS5 proxy router** on a single port that routes each connection through the right tunnel based on hostname pattern
- **Pattern matching** — `*.example.com`, `10.0.1.*`, exact hosts, and `*` catch-all; first match wins
- **SSH agent support** — works with YubiKey, gpg-agent, and ssh-agent out of the box
- **Hot reload** — send `SIGHUP` to apply config changes without restart
- **Admin UI** — built-in web dashboard with live traffic graphs per tunnel
- **Prometheus metrics** — `/metrics` endpoint with bytes, active connections, and reconnect counters
- **Health endpoint** — `GET /health` for load balancers and container probes
- **Multiarch Docker image** — `linux/amd64` and `linux/arm64`

## Installation

### From source

```bash
git clone https://github.com/pottom/hopscotch.git
cd hopscotch
./build.sh binary
# binary is at dist/hopscotch
```

### Docker

```bash
docker pull ghcr.io/pottom/hopscotch:latest
```

## Quick start

1. Create `hopscotch.yaml` (see [Configuration](#configuration) below)
2. Trust your SSH hosts:
   ```bash
   hopscotch trust all
   ```
3. Start:
   ```bash
   hopscotch start
   ```
4. Use the proxy:
   ```bash
   export ALL_PROXY=socks5h://localhost:8080
   curl https://internal.service.example.com
   ```

## Configuration

```yaml
tunnels:
  - name: prod-jump
    host: 10.0.0.1
    port: 22
    user: myuser
    local_port: 1080
    # identity_file: ~/.ssh/id_rsa   # omit to use SSH agent (YubiKey, gpg-agent)
    dial_timeout: 15                 # seconds; TCP connect + SSH handshake
    keepalive_interval: 30           # seconds between keepalive probes
    keepalive_max_fails: 3           # consecutive failures → reconnect
    reconnect_delay: 5               # initial backoff (doubles each attempt, max 2m)

  - name: staging-jump
    host: 10.0.1.1
    port: 22
    user: myuser
    identity_file: ~/.ssh/id_rsa
    local_port: 1081

proxy:
  port: 8080
  rules:
    - pattern: "*.prod.internal"
      tunnel: prod-jump
    - pattern: "*.staging.internal"
      tunnel: staging-jump
    - pattern: "*"
      via: direct          # everything else goes direct

admin:
  port: 9090
  bind: "127.0.0.1"       # change to 0.0.0.0 to expose in containers
```

### Tunnel options

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `name` | ✓ | — | Unique tunnel name, used in proxy rules |
| `host` | ✓ | — | SSH server hostname or IP |
| `port` | | `22` | SSH server port |
| `user` | ✓ | — | SSH username |
| `identity_file` | | — | Path to private key; omit to use SSH agent |
| `local_port` | ✓ | — | Local SOCKS5 port for this tunnel |
| `dial_timeout` | | `30` | SSH TCP connect + handshake timeout in seconds |
| `keepalive_interval` | | `30` | Keepalive probe interval in seconds |
| `keepalive_max_fails` | | `3` | Consecutive failures before reconnect |
| `reconnect_delay` | | `5` | Initial reconnect delay in seconds (doubles each attempt) |
| `reconnect_max_delay` | | `30` | Reconnect backoff cap in seconds |

### Proxy rules

Rules are evaluated top-to-bottom; the first match wins.

| Pattern | Example | Matches |
|---------|---------|---------|
| Wildcard prefix | `*.example.com` | `foo.example.com`, `bar.example.com` |
| Wildcard suffix | `10.0.1.*` | `10.0.1.1`, `10.0.1.254` |
| Exact | `db.internal` | `db.internal` only |
| Catch-all | `*` | everything |

`via: direct` sends the connection through without tunneling.

## Commands

```
hopscotch start              # start daemon (detaches from terminal)
hopscotch start --foreground # stay in foreground (for Docker, systemd)
hopscotch start --restart    # replace a running instance without prompting
hopscotch stop               # stop the daemon
hopscotch status             # show live tunnel and proxy status
hopscotch trust <name|host|all>  # add SSH host key to known_hosts
hopscotch validate           # validate the config file
hopscotch version            # print version information
```

Global flags:
```
--config <path>    path to config file (default: hopscotch.yaml)
--log-level        debug | info | warn | error (default: info)
```

## Using the proxy

Set the `ALL_PROXY` environment variable so all tools use hopscotch automatically:

```bash
export ALL_PROXY=socks5h://localhost:8080
```

The `socks5h` scheme means DNS is resolved through the proxy (inside the tunnel), which is required for internal hostnames.

Per-request override:

```bash
curl -x socks5h://localhost:8080 https://internal.service.example.com
```

## Admin UI

The web dashboard is available at `http://localhost:9090` (or whichever port `admin.port` is set to).

![hopscotch admin UI](docs/ui-preview.svg)

Each tunnel gets its own full-width card showing:

- **Status** — animated dot: green (connected), amber blinking (connecting), red (disconnected)
- **Host** — the SSH server address
- **SOCKS5 port** — the local port for this tunnel
- **Uptime** — how long the tunnel has been connected in the current session
- **Reconnect countdown** — when connecting, shows _next in Ns_ so you know when the next attempt fires
- **Reconnect count** — total reconnects since start
- **Live throughput** — ↓ bytes/s in and ↑ bytes/s out, updated every second via SSE
- **Active connections** — current number of open connections through this tunnel
- **Sparkline chart** — 60-second rolling traffic graph (Chart.js, no page reloads)

A **direct** card at the bottom tracks connections that bypassed the tunnels (matched a `via: direct` rule or the catch-all fallback).

The UI is powered by [Alpine.js](https://alpinejs.dev/) and updates in-place via Server-Sent Events — no polling, no full-page refreshes, no choppy redraws.

### API endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Returns `{"status":"ok"}` — suitable for health checks |
| `GET /status` | Full JSON status of all tunnels and the proxy |
| `GET /metrics` | Prometheus-compatible metrics (see below) |
| `GET /traffic/stream` | SSE stream of per-second traffic deltas |

## Prometheus metrics

The `/metrics` endpoint exposes metrics in the Prometheus text format:

| Metric | Type | Description |
|--------|------|-------------|
| `hopscotch_tunnel_status` | gauge | `1` = connected, `0` = other; label `tunnel` |
| `hopscotch_tunnel_uptime_seconds` | gauge | Seconds since last connect |
| `hopscotch_tunnel_reconnects_total` | counter | Total reconnect attempts |
| `hopscotch_tunnel_bytes_in_total` | counter | Cumulative bytes received through tunnel |
| `hopscotch_tunnel_bytes_out_total` | counter | Cumulative bytes sent through tunnel |
| `hopscotch_tunnel_active_connections` | gauge | Current open connections |
| `hopscotch_direct_bytes_in_total` | counter | Cumulative bytes received via direct |
| `hopscotch_direct_bytes_out_total` | counter | Cumulative bytes sent via direct |
| `hopscotch_direct_active_connections` | gauge | Current direct open connections |

Example PromQL for live throughput:

```promql
rate(hopscotch_tunnel_bytes_in_total{tunnel="prod-jump"}[1m])
```

## Docker

```bash
docker run -d \
  -v $(pwd)/hopscotch.yaml:/etc/hopscotch/config.yaml:ro \
  -v ~/.ssh/known_hosts:/home/hopscotch/.ssh/known_hosts:ro \
  -v ~/.ssh/id_rsa:/etc/hopscotch/keys/id_rsa:ro \
  -p 8080:8080 \
  -p 9090:9090 \
  ghcr.io/pottom/hopscotch:latest
```

Or with docker compose — see [`deploy/docker-compose.yml`](deploy/docker-compose.yml).

In containers, the process runs in foreground mode automatically (`--foreground` is set in the image entrypoint). Set `admin.bind: "0.0.0.0"` so the mapped port is reachable from the host.

## Building

```bash
./build.sh binary        # local binary → dist/hopscotch
./build.sh binary-all    # all platforms → dist/
./build.sh container     # multiarch Docker image (local, no push)
./build.sh publish       # build + push to ghcr.io
./build.sh release       # binary-all + publish
```

For `publish`, set:
```bash
export GITHUB_USER=pottom
export GITHUB_TOKEN=<token with write:packages scope>
```

## Environment variables

| Variable | Description |
|----------|-------------|
| `HOPSCOTCH_CONFIG` | Path to config file |
| `HOPSCOTCH_INSECURE_SKIP_KNOWN_HOSTS` | Set to `true` to disable host key verification (not recommended) |
| `SSH_AUTH_SOCK` | SSH agent socket — set automatically by ssh-agent, gpg-agent |
| `ALL_PROXY` | Proxy URL for outgoing connections from other tools |

## License

MIT
