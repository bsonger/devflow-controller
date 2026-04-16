# Constraints

## Goal

Keep `devflow-controller` as a cluster-state synchronization worker rather than a workflow engine.

## Hard constraints

- the durable write target is Mongo-backed `manifest.steps` and `job.steps` state
- resource correlation depends on labels such as `tekton.dev/pipelineRun` and DevFlow job identifiers; unmatched resources are skipped
- step updates must remain monotonic enough to avoid reopening already terminal `failed` or `succeeded` steps
- missing job steps may be created during sync, but the controller must not invent higher-level release plans
- config loading requires `log` and `mongo` sections; kube access falls back from local kubeconfig to in-cluster config
- `docs/cd/_index.md` remains the repo-local authority for CD boundary tightening and known gaps between intended scope and code reality

## Non-goals

- public API ownership
- release orchestration ownership
- manifest/job business-semantic ownership outside synchronized step state
