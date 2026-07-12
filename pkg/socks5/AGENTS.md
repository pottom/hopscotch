# pkg/socks5

## Purpose

A minimal, dependency-free SOCKS5 protocol server (RFC 1928: auth negotiation, `CONNECT` handling) used by `internal/proxy`.

## Ownership

`auth.go`, `handler.go`, `server.go`.

## Local Contracts

- This package lives under `pkg/`, not `internal/`, specifically so it's importable/reusable on its own (published on pkg.go.dev). Exported identifiers need real godoc comments, and a breaking change here is a breaking change for external importers — not just an internal refactor.

## Work Guidance

(none beyond the above)

## Verification

`go test ./pkg/socks5/...`
