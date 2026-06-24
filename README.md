<div align="center">
<img src="docs/logo.svg" width="96" alt="hopscotch logo">

# hopscotch

> Your SSH tunnels, managed. Dead connections detected in seconds, not minutes. Every request routed to the right tunnel — automatically.

</div>

[![Release](https://img.shields.io/github/v/release/pottom/hopscotch?color=38bdf8&label=latest)](https://github.com/pottom/hopscotch/releases/latest)
[![License](https://img.shields.io/badge/license-MIT-818cf8)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8)](go.mod)

> The name comes from the children's game: just as a player hops from square to square along a fixed path, hopscotch routes each connection through the right tunnel — automatically, one hop at a time.

![hopscotch TUI dashboard](docs/tui-status.svg)

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
```bash
hopscotch enable
# then just open http://grafana.infra.internal in your browser
```

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
| **TUI dashboard** | Live tunnel cards with dual-channel traffic graphs, reconnect countdowns, URL tester, log streaming. |
| **Web UI** | Same data, in your browser at `localhost:9090`, live SSE updates, no page refresh needed. |
| **VPN integration** | Manages openconnect as a subprocess; tunnels wait for VPN before connecting, show reason in UI. |
| **Hot reload** | Config reloads on `SIGHUP` or file change, tunnels re-configured in place, no restart. |
| **Self-update** | `hopscotch update` atomically replaces the binary. Container-aware — prints a notice instead of updating inside Docker. |
| **Prometheus metrics** | `/metrics` endpoint with per-tunnel bytes, connections, reconnects, uptime. |

One binary. Zero services. Zero background daemons beyond itself.

## TUI dashboard

`hopscotch status` opens a live terminal dashboard. Four tabs: **Status**, **Patterns**, **Logs**, **Docs**.

![TUI status tab](docs/tui-status.svg)

Each tunnel card shows: connection status, host, local port, uptime, reconnect counter, live per-second throughput (↓ in / ↑ out), active connection count, and a reason line when something's wrong — like `waiting for VPN: corp-vpn`. Traffic graphs update every second. A `⚡v0.5.1` badge appears next to the version when an update is available.

### TUI key bindings

| Key | Action |
|-----|--------|
| `Tab` / `s` / `l` / `p` | Switch tabs: Status → Logs → Patterns → Docs |
| `↑` `↓` / `j` `k` | Scroll |
| `/` | Focus URL tester (Patterns tab) |
| `Esc` | Unfocus tester |
| `f` | Toggle graphs on/off (compact mode) |
| `g` | Toggle mirror graph (dual-channel ↔ download only) |
| `q` / `Ctrl+C` | Quit |

## Routing patterns

The **Patterns tab** shows exactly which hostnames route where. Press `/` to focus the URL tester — type any hostname or URL and hopscotch highlights the matching rule in real time.

![TUI routes tab with URL tester](docs/tui-routes.svg)

Rules evaluate top-to-bottom; first match wins. Patterns support:

| Pattern | Example | Matches |
|---------|---------|---------|
| Wildcard prefix | `*.example.com` | `foo.example.com`, `bar.example.com` |
| Wildcard suffix | `10.0.1.*` | `10.0.1.1` … `10.0.1.254` |
| Exact | `db.internal` | `db.internal` only |
| Catch-all | `*` | everything |

`via: direct` bypasses all tunnels. Put it last as the fallback.

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
# Re-run after changing proxy rules, or let hopscotch refresh it on config reload.

Host *.prod.internal 10.0.1.*
    ProxyCommand hopscotch proxy-connect %h %p

Host *.staging.internal
    ProxyCommand hopscotch proxy-connect %h %p
```

hopscotch then asks whether to add the `Include` line to `~/.ssh/config` automatically. Say `y` and you're done — or add it manually:

```bash
echo 'Include ~/.config/hopscotch/ssh_config' >> ~/.ssh/config
```

The generated file stays in sync: hopscotch refreshes it automatically on every config reload (SIGHUP or file change). Re-run `--write` after rule changes to update the include immediately.

## Web admin UI

`http://localhost:9090` — mirrors the TUI with tunnel cards, live traffic graphs, a Patterns tab with interactive URL tester, VPN status, and a Logs tab with real-time structured output. Pure SSE, no polling.

![Admin web UI](docs/ui-preview.svg)

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

1. Copy and edit the example config:
   ```bash
   cp hopscotch.example.yaml hopscotch.yaml
   ```

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
  no_proxy: "localhost,127.0.0.1,::1"
  rules:
    - pattern: "*.prod.internal"
      tunnel: prod-jump
    - pattern: "*.staging.internal"
      tunnel: staging-jump
    - pattern: "*"
      via: direct

admin:
  port: 9090
  bind: "127.0.0.1"    # set to 0.0.0.0 to expose in containers
```

### Tunnel options

| Field | Default | Description |
|-------|---------|-------------|
| `name` | — | Unique tunnel name (referenced in proxy rules) |
| `host` | — | SSH server hostname or IP |
| `port` | `22` | SSH server port |
| `user` | — | SSH username |
| `identity_file` | — | Path to private key; omit to use SSH agent (YubiKey, gpg-agent, ssh-agent) |
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
    pre_connect:
      - "sudo networksetup -setdnsservers Wi-Fi Empty"
    reconnect_delay: 15
    reconnect_max_delay: 120

tunnels:
  - name: prod-jump
    requires_vpn: corp-vpn        # won't connect until VPN is up
    host: 10.0.0.10
    ...
```

hopscotch validates that `sudo` can run openconnect before daemonizing, and kills the entire openconnect process group on shutdown.

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
| `server` | — | VPN server URL |
| `user` | — | VPN username |
| `authgroup` | — | Authentication group / realm |
| `sudo` | `false` | Run openconnect via `sudo` |
| `binary` | `openconnect` | Path to openconnect binary |
| `password_env` | — | Environment variable name containing the password |
| `password_cmd` | — | Shell command whose stdout is the password |
| `ping_host` | — | `host:port` TCP probe to confirm VPN is up |
| `pre_connect` | — | Shell commands to run before each connection attempt |
| `post_disconnect` | — | Shell commands to run after each VPN disconnect (runs even on shutdown) |
| `extra_args` | — | Additional openconnect flags |
| `reconnect_delay` | `15` | Initial reconnect backoff (seconds) |
| `reconnect_max_delay` | `120` | Reconnect backoff cap (seconds) |

## Commands

```
hopscotch start                    # start daemon (detaches from terminal)
hopscotch start --foreground       # stay in foreground (for Docker, systemd)
hopscotch start --restart          # replace running instance without prompting
hopscotch stop                     # stop the daemon
hopscotch status                   # open interactive TUI (plain text when piped)
hopscotch status --plain           # force plain text (useful for scripts, watch)
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
