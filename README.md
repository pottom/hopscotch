<div align="center">
<img src="docs/logo.svg" width="96" alt="hopscotch logo">

# hopscotch

> Your SSH tunnels, managed. Dead connections detected in seconds, not minutes. Every request routed to the right tunnel — automatically.

</div>

[![Release](https://img.shields.io/github/v/release/pottom/hopscotch?color=38bdf8&label=latest)](https://github.com/pottom/hopscotch/releases/latest)
[![License](https://img.shields.io/badge/license-MIT-818cf8)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8)](go.mod)

> The name comes from the children's game: just as a player hops from square to square along a fixed path, hopscotch routes each connection through the right tunnel — automatically, one hop at a time.

![hopscotch TUI dashboard](docs/tui-status.png)

---

## The problem

Your infrastructure is behind a VPN and a jump host. The "official" way to get anything done is either SSH into a Linux bastion and work with whatever tools are installed there, or RDP into a Windows jump server and work in a slow remote desktop with someone else's browser and no access to your own tools.

Want to copy a file to a production server?
```bash
# Step 1 — copy to the jump host
scp report.csv user@jump.corp:/tmp/
# Step 2 — from the jump, copy to the destination
ssh user@jump.corp "scp /tmp/report.csv user@prod-01.internal:/data/"
```

Want to open an internal web UI to configure a device? RDP in, wait for the remote desktop to load, use the laggy browser, squint at the small screen.

Want to edit code on a remote server? `vim`. No IntelliSense. No extensions. No your-own-terminal.

And if you have two bastions — one for prod, one for staging — you're manually switching `HTTP_PROXY` between `localhost:1080` and `localhost:1081` every time you switch context. And hoping you don't forget.

**hopscotch fixes all of this.** You run it on your own machine. Your own tools — browser, VS Code, rsync, Ansible, DBeaver, Postman — connect directly to internal hosts as if you were already there. The jump host becomes invisible.

The key insight: **one proxy port, pattern-based routing.** You define rules like `*.prod.internal → prod-jump` and `*.staging.internal → staging-jump`, and hopscotch figures out which tunnel to use for every request — automatically, per connection. You set `HTTP_PROXY=localhost:8080` once and never touch it again.

**Copy a file to production:**
```bash
rsync -av report.csv user@prod-01.internal:/data/
```

**Open an internal web UI in your own browser:**

Configure your browser to use SOCKS5 proxy at `localhost:8080`, or use [FoxyProxy](https://getfoxyproxy.org/) to route only internal hostnames through it — your public browsing stays direct.

**Edit code on a remote server:**
```
VS Code → Remote-SSH: Connect to Host → user@prod-01.internal
```

One binary. One config file. Start it once and stop thinking about infrastructure plumbing.

## What you get

| | |
|---|---|
| **One proxy, smart routing** | One SOCKS5 port for everything. Pattern rules (`*.prod.internal → prod-jump`, `*.staging.internal → staging-jump`) decide per-request which tunnel to use. Set `HTTP_PROXY` once, never touch it again. |
| **Dead connection detection** | Keepalive probes every few seconds — not TCP's minutes-long timeout. Reconnect starts in under 10 seconds. |
| **Shell integration** | `hopscotch enable` / `disable` like Python venv — sets and restores `HTTP_PROXY` without touching other shells. |
| **SSH ProxyCommand** | `ssh`, `scp`, `rsync`, VSCode Remote, Ansible — all route through tunnels transparently, zero extra flags. |
| **TUI dashboard** | Live tunnel cards with dual-channel traffic graphs, reconnect countdowns, URL tester, log streaming with level filter. |
| **Web UI** | Same data, in your browser at `localhost:9090`, live SSE updates, log level filter, tab and level persisted across reloads. |
| **VPN integration** | Manages openconnect as a subprocess; tunnels wait for VPN before connecting, show reason in UI. |
| **Hot reload** | Config reloads on `SIGHUP` or file change, tunnels re-configured in place, no restart. |
| **Self-update** | `hopscotch update` atomically replaces the binary. Container-aware — prints a notice instead of updating inside Docker. |
| **Force reconnect** | `r` in TUI or ↻ button in web UI reconnects a tunnel immediately, skipping the backoff timer. |
| **Prometheus metrics** | `/metrics` endpoint with per-tunnel bytes, connections, reconnects, keepalive failures, uptime. |

One binary. Zero services. Zero background daemons beyond itself.

## How it works

![Architecture overview](docs/arch-overview.svg)

hopscotch sits between your tools and your jump hosts. Apps connect to a single local SOCKS5 port; the rule engine walks the pattern list (first match wins) and dispatches each connection to the right SSH tunnel. The tunnel pool stays connected in the background — reconnecting automatically when a link drops.

## TUI dashboard

`hopscotch status` opens a live terminal dashboard. Four tabs: **Status**, **Patterns**, **Logs**, **Docs**.

![TUI status tab](docs/tui-status.png)

Each tunnel shows: connection status, host, local port, uptime, reconnect counter, cumulative bytes transferred (↓ in / ↑ out since process start), and active connection count. When a graph is open, the live per-second rate appears above the braille graph. A reason line appears when something's wrong — like `waiting for VPN: corp-vpn`. A `⚡v0.8.0` badge appears next to the version when an update is available.

### TUI key bindings

| Key | Action |
|-----|--------|
| `Tab` / `s` / `l` / `p` | Switch tabs: Status → Logs → Patterns → Docs |
| `↑` `↓` / `j` `k` | **Status tab:** move cursor between tunnels (viewport follows) · **Other tabs:** scroll |
| `r` | **Status tab:** force reconnect selected tunnel immediately (skips backoff) |
| `/` | Focus URL tester (Patterns tab) |
| `Esc` | Unfocus tester |
| `f` | **Status tab:** toggle graphs on/off (compact mode) · **Logs tab:** cycle log level filter (ALL → INFO+ → WARN+ → ERR) |
| `g` | Toggle mirror graph (dual-channel ↔ download only) |
| `q` / `Ctrl+C` | Quit |

## Routing patterns

The **Patterns tab** shows exactly which hostnames route where. Press `/` to focus the URL tester — type any hostname or URL and hopscotch highlights the matching rule in real time.

The **Logs tab** shows a live stream of structured log lines, filterable by level with `f`.

![TUI logs tab](docs/tui-logs.png)

Rules evaluate top-to-bottom; first match wins. Patterns support:

| Pattern | Example | Matches |
|---------|---------|---------|
| Wildcard prefix | `*.example.com` | `foo.example.com`, `bar.example.com` |
| Wildcard suffix | `10.0.1.*` | `10.0.1.1` … `10.0.1.254` |
| CIDR block | `10.0.1.0/24` | any IP in that subnet |
| Exact | `db.internal` | `db.internal` only |
| Catch-all | `*` | everything |

`target: direct` bypasses all tunnels. `target: block` refuses the connection. Put a catch-all last as the fallback.

![Connection flow](docs/flow-connection.svg)

CIDR patterns are precise and composable — `10.0.0.0/8` covers an entire private range where `10.*.*.*` would need four octets. They can be mixed freely with glob patterns in the same rule list.

## Shell integration

Works like Python venv — `enable` captures and exports the proxy env, `disable` restores exactly what was there before. Other open shells are unaffected.

![Shell integration demo](docs/shell-demo.svg)

Add once to `~/.zshrc` or `~/.bashrc`:

```bash
eval "$(hopscotch shell-init)"
```

Then toggle per-shell:

```bash
hopscotch enable    # sets HTTP_PROXY, HTTPS_PROXY, NO_PROXY in this shell
hopscotch disable   # restores the previous environment exactly
```

When the proxy is active, `HOPSCOTCH_ACTIVE` is set — use it in your prompt or scripts. For a one-off without shell-init: `eval "$(hopscotch enable)"`.

## SSH ProxyCommand integration

This is where it gets powerful. hopscotch can act as an SSH `ProxyCommand`, routing any SSH-based tool through your tunnels — transparently, without extra flags on every command.

Once set up, these all just work:

```bash
ssh user@10.0.1.50               # through the tunnel, no -o ProxyCommand needed
scp report.csv user@10.0.1.50:/data/
rsync -av ./app/ user@10.0.1.50:/srv/app/
```

**VSCode Remote SSH:** open Command Palette → *Connect to Host* → type `user@10.0.1.50`. VSCode tunnels through hopscotch automatically.

**Ansible:** works out of the box if your inventory hosts match a proxy rule pattern. No `ansible.cfg` changes.

### Setup (one time)

```bash
hopscotch ssh-config --write
```

This writes `~/.config/hopscotch/ssh_config` — an SSH config snippet generated from your proxy rules:

```
# Generated by hopscotch ssh-config --write

Host *.prod.internal 10.0.1.*
    ProxyCommand hopscotch proxy-connect %h %p

Host *.staging.internal
    ProxyCommand hopscotch proxy-connect %h %p
```

CIDR patterns (`10.0.1.0/24`) work for routing but cannot be expressed as SSH `Host` patterns. The generated file notes them as comments and skips them from the `Host` line — cover those ranges with `Match originalhost` in your `~/.ssh/config.d/` files instead (see [SSH config integration](https://github.com/pottom/hopscotch/wiki/Integrations#ssh-config-and-the-generated-proxycommand)).

hopscotch then asks whether to add the `Include` line to `~/.ssh/config` automatically. Say `y` and you're done — or add it manually:

```bash
echo 'Include ~/.config/hopscotch/ssh_config' >> ~/.ssh/config
```

The generated file stays in sync: hopscotch refreshes it automatically on every config reload (SIGHUP or file change). Re-run `--write` after rule changes to update the include immediately.

## Web admin UI

`http://localhost:9090` — mirrors the TUI with tunnel cards, live traffic graphs, a Patterns tab with interactive URL tester, VPN status, and a Logs tab with real-time structured output. Pure SSE, no polling.

The **Logs tab** has level filter buttons (ALL / INFO / WARN / ERR) that reconnect the SSE stream so only matching lines are sent from the server. The active tab and selected log level are saved in `localStorage` and restored on reload.

![Admin web UI — Status](docs/ui-status.png)

![Admin web UI — Logs](docs/ui-logs.png)

## Installation

### One-liner

```bash
curl -fsSL https://raw.githubusercontent.com/pottom/hopscotch/main/install.sh | bash
```

Detects platform (macOS/Linux, amd64/arm64) and installs the latest release to `/usr/local/bin`.

### Self-update

```bash
hopscotch update          # check and update
hopscotch update --check  # check only, print version if newer
```

Atomically replaces the binary. Prints an explicit message instead of silently updating when running inside a container.

### From source

```bash
git clone https://github.com/pottom/hopscotch.git
cd hopscotch
./build.sh binary   # → dist/hopscotch
```

### Docker

```bash
docker pull ghcr.io/pottom/hopscotch:latest
```

## Quick start

1. Create the config directory and write a minimal config:
   ```bash
   mkdir -p ~/.config/hopscotch
   ```
   Then create `~/.config/hopscotch/config.yaml` — see the [Configuration](#configuration) section below for a minimal example, or download the [annotated example](https://raw.githubusercontent.com/pottom/hopscotch/main/hopscotch.example.yaml) for all available options.

2. Trust your SSH hosts (first run):
   ```bash
   hopscotch trust all
   ```

3. Load shell integration (once, in `~/.zshrc` or `~/.bashrc`):
   ```bash
   eval "$(hopscotch shell-init)"
   ```

4. Start:
   ```bash
   hopscotch start
   ```

5. Route traffic:
   ```bash
   # per-request
   curl -x socks5h://localhost:8080 https://internal.service.corp

   # or activate for the whole shell session
   hopscotch enable
   curl https://internal.service.corp
   ```

6. Open the dashboard:
   ```bash
   hopscotch status        # TUI
   open http://localhost:9090  # web UI
   ```

> **Tutorials, troubleshooting, and integration guides** are in the [wiki](https://github.com/pottom/hopscotch/wiki).

## Configuration

See [`hopscotch.example.yaml`](hopscotch.example.yaml) for a full annotated example. Minimal working config:

```yaml
tunnels:
  - name: prod-jump
    host: bastion.corp
    port: 22
    user: alice
    local_port: 1080
    keepalive_interval: 5      # seconds between keepalive probes
    keepalive_max_fails: 2     # consecutive failures → reconnect
    reconnect_delay: 5         # initial backoff (doubles each retry)
    reconnect_max_delay: 30    # cap

  - name: staging-jump
    host: dev.corp
    port: 22
    user: alice
    identity_file: ~/.ssh/id_rsa
    local_port: 1081

proxy:
  port: 8080
  no_proxy: "localhost,127.0.0.1,::1"   # excluded from HTTP_PROXY when using `hopscotch enable`
  # username: alice                      # optional: require SOCKS5 authentication
  # password: secret
  rules:
    - pattern: "*.prod.internal"
      target: prod-jump
    - pattern: "*.staging.internal"
      target: staging-jump
    - pattern: "*"
      target: direct

admin:
  port: 9090
  bind: "127.0.0.1"    # set to 0.0.0.0 to expose in containers
  # username: alice    # optional: protect the web UI and TUI with a password
  # password: secret
```

### Tunnel options

| Field | Default | Description |
|-------|---------|-------------|
| `name` | — | Unique tunnel name (referenced in proxy rules) |
| `host` | — | SSH server hostname or IP |
| `port` | `22` | SSH server port |
| `user` | — | SSH username |
| `identity_file` | — | Path to private key; omit to use SSH agent (YubiKey, gpg-agent, ssh-agent). On auth failure, hopscotch watches the agent for new keys and retries immediately when one appears — no need to wait for the backoff timer after inserting a YubiKey. |
| `local_port` | — | Local SOCKS5 port for this tunnel |
| `requires_vpn` | — | Name of a `vpn` entry; tunnel waits for VPN before connecting |
| `pre_connect` | — | Shell commands to run before each dial attempt |
| `dial_timeout` | `30` | TCP connect + SSH handshake timeout (seconds) |
| `keepalive_interval` | `5` | Keepalive probe interval (seconds) |
| `keepalive_max_fails` | `2` | Consecutive failures before reconnect |
| `reconnect_delay` | `5` | Initial reconnect backoff (doubles each attempt) |
| `reconnect_max_delay` | `30` | Reconnect backoff cap (seconds) |
| `force_pty` | `false` | Open a PTY shell session — for jump hosts that enforce channel policies (SPS/SCB appliances) |

### VPN integration

hopscotch can manage an **openconnect** VPN subprocess and make tunnels wait for it before connecting. The TUI and web UI show `waiting for VPN: corp-vpn` on any tunnel that's blocked.

```yaml
vpn:
  - name: corp-vpn
    type: openconnect
    server: https://vpn.corp.com
    authgroup: "Engineering"
    user: alice
    sudo: true                    # run openconnect via sudo
    ping_host: "10.0.0.1:22"     # TCP probe to confirm VPN is actually up
    reconnect_delay: 15
    reconnect_max_delay: 120

tunnels:
  - name: prod-jump
    requires_vpn: corp-vpn        # won't connect until VPN is up
    host: 10.0.0.10
    ...
```

hopscotch validates that `sudo` can run openconnect before daemonizing, and kills orphaned openconnect processes on startup and shutdown — using SIGTERM first so the VPN server receives a clean disconnect before SIGKILL, avoiding the 30-second session hold that some VPN servers impose after an abrupt drop.

**DNS resolution:** before starting openconnect, hopscotch resolves the VPN server hostname using a public DNS server (default: `1.1.1.1:53`), bypassing the system resolver. This prevents connect failures when the system DNS is left pointing at VPN-internal servers from a previous session — a common issue when vpnc-script sets DNS on connect but doesn't restore it after an abrupt disconnect. The resolved IP is forwarded to openconnect via `--resolve` so TLS certificate validation still uses the original hostname. Override with `dns_resolver: "8.8.8.8:53"` if needed.

**Route cleanup:** on disconnect, hopscotch automatically removes any routes left on the VPN tunnel interface (`utun*` on macOS, `tun*` on Linux). No `post_disconnect` cleanup scripts needed for this.

**Progress visibility:** the TUI MESSAGE column shows each connecting phase in real time — `resolving vpn.corp.com`, `openconnect starting`, `waiting for VPN tunnel`, `probing 10.0.0.1:22` — so you can see exactly where a slow or failed connect is stuck. The IFACE column shows the detected tunnel interface (e.g. `utun4`) once connected.

#### VPN password

Three options in priority order:

| Option | How |
|--------|-----|
| `password_env: VAR` | Read from environment variable — ideal for containers and systemd units |
| `password_cmd: "..."` | Run any shell command; its stdout becomes the password |
| OS keychain *(default)* | macOS Keychain / Linux Secret Service; store with `hopscotch vpn password <name>` |

`password_cmd` works with any secret store:
```yaml
password_cmd: "pass vpn/corp"
password_cmd: "gpg --decrypt /etc/hopscotch/vpn.pass.gpg"
password_cmd: "vault kv get -field=password secret/vpn"
password_cmd: "cat /run/secrets/vpn_pass"   # Docker / Kubernetes secret mount
```

#### VPN options

| Field | Default | Description |
|-------|---------|-------------|
| `name` | — | Unique VPN name (referenced by `requires_vpn`) |
| `type` | — | `openconnect` (only supported type) |
| `server` | — | VPN server URL (hostname preferred; hopscotch resolves it via `dns_resolver`) |
| `user` | — | VPN username |
| `authgroup` | — | Authentication group / realm |
| `sudo` | `false` | Run openconnect via `sudo` |
| `binary` | `openconnect` | Path to openconnect binary |
| `dns_resolver` | `1.1.1.1:53` | DNS server used for pre-connect hostname resolution; bypasses system DNS |
| `password_env` | — | Environment variable name containing the password |
| `password_cmd` | — | Shell command whose stdout is the password |
| `ping_host` | — | `host:port` TCP probe to confirm VPN is up |
| `pre_connect` | — | Shell commands to run before each connection attempt |
| `post_disconnect` | — | Shell commands to run after each VPN disconnect; route cleanup is automatic, rarely needed |
| `extra_args` | — | Additional openconnect flags |
| `reconnect_delay` | `15` | Initial reconnect backoff (seconds) |
| `reconnect_max_delay` | `120` | Reconnect backoff cap (seconds) |

### Sharing the proxy on your network

Run hopscotch on one machine with `proxy.bind: 0.0.0.0` and every other device on your network can use it as a shared SOCKS5 proxy — no VPN client or SSH config needed on each device.

![Shared proxy for the whole network](docs/shared-proxy.svg)

```yaml
proxy:
  port: 1080
  bind: 0.0.0.0   # accept LAN connections, not just localhost
```

On each other machine:

```bash
export ALL_PROXY=socks5h://192.168.1.10:1080
```

Replace `192.168.1.10` with the local IP of the machine running hopscotch.

## Commands

```
hopscotch start                    # start daemon (detaches from terminal)
hopscotch start --foreground       # stay in foreground (for Docker, systemd)
hopscotch start --restart          # replace running instance; finds stale processes by admin port if PID file is missing
hopscotch stop                     # stop the daemon
hopscotch status                             # open interactive TUI (plain text when piped)
hopscotch status --plain                     # force plain text (useful for scripts, watch)
hopscotch status --username alice            # authenticate if admin auth is enabled
hopscotch status --username alice --password secret  # non-interactive (or pipe password via stdin)
hopscotch logs                     # stream live log output from the daemon
hopscotch enable                   # activate proxy in current shell
hopscotch disable                  # deactivate proxy, restore previous env
hopscotch shell-init               # print shell integration (eval once in .zshrc)
hopscotch vpn password <name>      # store or update VPN password in OS keychain
hopscotch update                   # check for newer release and update the binary
hopscotch update --check           # check only, do not download
hopscotch trust <name|host|all>    # add SSH host key to known_hosts
hopscotch validate                 # validate the config file without starting
hopscotch version                  # print version info
hopscotch ssh-config               # print SSH ProxyCommand config block
hopscotch ssh-config --write       # write to ~/.config/hopscotch/ssh_config
hopscotch proxy-connect <host> <port>  # SOCKS5 stdio bridge (used as SSH ProxyCommand)
```

Global flags: `--config <path>` · `--verbose` · `--log-file <path>`

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

Or with Compose — see [`deploy/docker-compose.yml`](deploy/docker-compose.yml).

Set `admin.bind: "0.0.0.0"` when running in a container so the admin port is reachable from the host. Use `password_env` or `password_cmd` for VPN credentials — no keychain available in containers.

## Prometheus metrics

Metrics at `/metrics` in Prometheus text format:

| Metric | Type | Description |
|--------|------|-------------|
| `hopscotch_tunnel_status` | gauge | `1` = connected; label `tunnel` |
| `hopscotch_tunnel_uptime_seconds` | gauge | Seconds since last connect |
| `hopscotch_tunnel_reconnects_total` | counter | Total reconnect attempts |
| `hopscotch_tunnel_bytes_in_total` | counter | Cumulative bytes received |
| `hopscotch_tunnel_bytes_out_total` | counter | Cumulative bytes sent |
| `hopscotch_tunnel_active_connections` | gauge | Current open connections |
| `hopscotch_tunnel_keepalive_failures` | gauge | Consecutive keepalive failures (resets on success/reconnect) |
| `hopscotch_direct_bytes_in_total` | counter | Bytes via direct path |
| `hopscotch_direct_bytes_out_total` | counter | Bytes via direct path |
| `hopscotch_direct_active_connections` | gauge | Current direct connections |
| `hopscotch_vpn_status` | gauge | `2` = connected, `1` = connecting, `0` = disconnected; label `vpn` |
| `hopscotch_vpn_uptime_seconds` | gauge | Seconds since VPN connected |
| `hopscotch_vpn_reconnects_total` | counter | Total VPN reconnect attempts |

```promql
rate(hopscotch_tunnel_bytes_in_total{tunnel="prod-jump"}[1m])
```

## Building

```bash
./build.sh binary        # local binary → dist/hopscotch
./build.sh binary-all    # all platforms → dist/
./build.sh install       # build + install to /usr/local/bin (macOS: ad-hoc signed)
./build.sh container     # multiarch Docker image (local, no push)
./build.sh publish       # build + push to ghcr.io
./build.sh release       # binary-all + publish
```

## License

MIT
