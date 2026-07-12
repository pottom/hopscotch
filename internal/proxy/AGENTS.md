# internal/proxy

## Purpose

The SOCKS5 request router: matches each destination host against `proxy.rules` (top-to-bottom, first match wins), dials via the matched tunnel/`direct`/`block`, tracks per-route connection counts and traffic.

## Ownership

`router.go` (`Router`, `DialContext`/`resolve`), `pattern.go` (`matchPattern`/`MatchPattern`/`IsCIDR`), `server.go` (SOCKS5 listener), `traffic.go` (`DirectCounter`/`TrafficSnapshot`).

## Local Contracts

- Rule matching is first-match-wins, top-to-bottom — precedence between overlapping CIDR/glob rules is the config author's responsibility; only pattern *syntax* is validated (`config.ValidatePattern`), not precedence.
- `MatchPattern` is the exported form of `matchPattern` used by the TUI/admin API for ad-hoc "test this URL against the rules" checks — keep it in sync with whatever `matchPattern` actually does; don't let a second implementation drift in.
- Every dialed connection logs exactly one line via `log.Info("proxy", "proto", ..., "host", ..., "pattern", ..., "via", ..., ["tunnel", ...], ["note", ...])`. This line is the only per-connection audit trail and is consumed as **raw text** by both the TUI and web UI log views (no field-level parsing on the UI side) — new fields appended here show up in both UIs automatically, no UI change required.
- `waitForTunnel` fails fast (no waiting) if the target tunnel isn't `Connected` — callers get an immediate error instead of hanging through a dial timeout.

## Work Guidance

(none beyond the above)

## Verification

`go test ./internal/proxy/...`
