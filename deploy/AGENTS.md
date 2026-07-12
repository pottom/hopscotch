# deploy

## Purpose

Container packaging for hopscotch: Dockerfile, docker-compose (dev and prod-like), example config for containerized deployments.

## Ownership

`Dockerfile`, `docker-compose.yml`, `docker-compose.dev.yml`, `config.example.yaml`.

## Local Contracts

- Containerized mode is detected at runtime via `HOPSCOTCH_CONTAINER=true` (`internal/state`), which redirects the PID file / cache-dir state file to `/tmp` instead of the user cache dir. Keep this env var set in these compose files — removing it re-enables the host-style "already running" PID check inside the container.
- `config.example.yaml` here is deployment-oriented and may drift in shape from the repo-root `hopscotch.example.yaml` (dev-oriented) — check both when documenting a new config field.

## Work Guidance

(none beyond the above)

## Verification

(none beyond building/running the compose files manually)
