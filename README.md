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
- **Admin UI** — built-in web dashboard at the admin port showing live tunnel status
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
| `keepalive_interval` | | `30` | Keepalive probe interval in seconds |
| `keepalive_max_fails` | | `3` | Consecutive failures before reconnect |
| `reconnect_delay` | | `5` | Initial reconnect delay in seconds (doubles each attempt, max 2 min) |

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

The web dashboard is available at `http://localhost:9090` (or whichever port `admin.port` is set to). It shows live tunnel status, uptime, reconnect counts, and proxy configuration. The page refreshes automatically every 5 seconds.

API endpoints:

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Returns `{"status":"ok"}` — suitable for health checks |
| `GET /status` | Full JSON status of all tunnels and the proxy |
| `GET /metrics` | Prometheus-compatible metrics |

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

Or with docker compose — see [`deploy/docker-compose.example.yml`](deploy/docker-compose.example.yml).

In containers, the process runs in foreground mode automatically (`--foreground` is set in the image entrypoint).

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
