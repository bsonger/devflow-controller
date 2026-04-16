# Architecture

## Purpose

`devflow-controller` is a cluster-state synchronizer for CD execution progress.
It listens to Tekton, Argo CD, Deployment, and Rollout resources, then writes normalized step progress back into Mongo-backed DevFlow records.

## Runtime flow

1. `cmd/main.go` loads YAML config from `config/` or `/etc/devflow-controller/config/`.
2. `pkg/bootstrap` initializes logging, optional OpenTelemetry, Mongo, and Kubernetes clients.
3. `pkg/controller/tekton.go` watches `TaskRun` and binds or updates manifest steps by `pipeline_id` and task name.
4. `pkg/controller/argo_cd.go` watches Argo `Application` and updates the apply step on the matching job.
5. `pkg/controller/job_resources.go` watches `Deployment` and `Rollout` resources, then updates deploy/canary/blue-green job steps.
6. `pkg/service/manifest` and `pkg/service/job` encapsulate Mongo step updates.

## Internal package layout

- `cmd/` — process startup
- `pkg/bootstrap/` — logger, otel, mongo, Tekton, and Argo client bootstrap
- `pkg/config/` — config loading and validation
- `pkg/controller/` — informer startup, event mapping, and step update orchestration
- `pkg/service/manifest/` — manifest lookup and step persistence
- `pkg/service/job/` — job lookup and step persistence
- `pkg/signal/` — signal-aware root context
- `docs/cd/` — current CD-oriented boundary notes and change templates

## External dependencies

- Mongo via `devflow-common/client/mongo`
- Tekton client and informers
- Argo CD client and informers
- Kubernetes `Deployment` informer plus dynamic `Rollout` informer
- shared DevFlow model types from `devflow-common/model`

## Non-goals

- exposing a public API surface
- owning release planning or workflow orchestration
- becoming the source of truth for manifest/job business semantics
- requiring callers to push step state through direct service APIs
