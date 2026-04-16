# Observability

## Purpose

`devflow-controller` is observable mainly through structured logs, Mongo step updates, and optional OpenTelemetry spans for terminal execution events.

## Logs

Important fields to keep in controller logs:
- `stage`
- `jobID` or `pipelineID`
- `taskName` / `stepName`
- `stepStatus`
- `progress`
- `app`, `namespace`, or rollout/deployment identifiers
- `error`

## Metrics

The current repo does not expose a dedicated Prometheus metrics endpoint.

Until one exists, operators should use:
- pod restart / crash metrics from Kubernetes
- Mongo persistence error logs
- step progression visibility in the consuming DevFlow UI or database

## Tracing

When trace annotations are present on Tekton `TaskRun` or Argo `Application` resources, the controller can create child spans for completed execution segments.

Operationally this means:
- missing trace annotations should not break sync
- traces are additive diagnostics, not a hard dependency for step persistence
- terminal event spans are the main tracing signal in the current codebase

## Health and readiness

The current implementation does not expose an HTTP `healthz` or `readyz` endpoint.

Health is inferred from:
- process liveness
- informer startup success
- ongoing step writes landing in Mongo

## Failure modes

Watch for:
- informer cache startup failures
- kubeconfig or in-cluster auth failures
- Mongo write failures during step updates
- label drift that breaks manifest/job correlation
- missing or inconsistent step names causing partial sync

## Dashboards and runbooks

Use shared Kubernetes pod health, Mongo dependency health, and release execution views until repo-specific controller dashboards exist.
