# devflow-controller

`devflow-controller` is the cluster-state synchronization worker for DevFlow CD progress.

## Role

- watch Tekton, Argo CD, Deployment, and Rollout resources
- map cluster execution state into Mongo-backed `manifest.steps` and `job.steps`
- keep controller scope focused on synchronization rather than orchestration
- avoid acting like a public API service

## Key Commands

- `bash scripts/verify.sh`
- `go test ./pkg/controller`
- `go test ./pkg/service/job`
- `go build ./...`

## Key Docs

- `AGENTS.md`
- `docs/README.md`
- `docs/architecture.md`
- `docs/constraints.md`
- `docs/observability.md`
- `docs/cd/_index.md`
- `docs/cd/change-request.md`
- `scripts/README.md`
