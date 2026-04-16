# AGENTS

## Startup
Read in this order:
1. `README.md`
2. `docs/architecture.md`
3. `docs/constraints.md`
4. `docs/observability.md`
5. `docs/cd/_index.md`

Public API: no.
This repo consumes cluster events and synchronizes Mongo-backed execution steps.
If ownership or cross-repo boundary questions appear, go back to `../devflow-control/docs/system/boundaries.md` and `../devflow-control/docs/services/release-service.md`.

## Commands
- `bash scripts/verify.sh`
- `go test ./pkg/controller`
- `go test ./pkg/service/job`
- `go build ./...`

## Before handoff
- Rerun `bash scripts/verify.sh`, plus any narrower `go test` commands useful for touched packages.
- Recheck `docs/cd/_index.md` if controller scope or CD writeback behavior changed.

## When to go back to devflow-control
Go back when repo ownership, controller boundaries, or cross-repo step semantics change.
