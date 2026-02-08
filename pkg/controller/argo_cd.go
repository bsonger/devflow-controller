package controller

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"reflect"
	"time"

	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	argoinformers "github.com/argoproj/argo-cd/v3/pkg/client/informers/externalversions"
	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/bsonger/devflow-common/client/argo"
	"github.com/bsonger/devflow-common/client/logging"
	"github.com/bsonger/devflow-common/model"
	"github.com/bsonger/devflow-controller/pkg/service/job"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"
)

// ============================
// Informer 启动入口
// ============================

func StartArgoApplicationInformer(ctx context.Context) error {

	factory := argoinformers.NewSharedInformerFactoryWithOptions(
		argo.ArgoCdClient,
		0,
	)
	informer := factory.Argoproj().V1alpha1().Applications().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			onApplication(ctx, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if reflect.DeepEqual(oldObj, newObj) {
				return
			}
			onApplication(ctx, newObj)
		},
	})

	go factory.Start(ctx.Done())

	cache.WaitForCacheSync(ctx.Done(), informer.HasSynced)
	return nil
}

// ============================
// 核心处理逻辑
// ============================

func onApplication(parentCtx context.Context, obj interface{}) {
	app, ok := obj.(*argov1alpha1.Application)
	if !ok || app == nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "argo.application.type_assert"),
		)
		return
	}

	jobID := app.Labels[model.JobIDLabel]
	if jobID == "" {
		logging.Logger.Debug("skip application sync due to missing job id label", zap.String("app", app.Name))
		return
	}
	cacheJobID(app.Name, jobID)

	// 生成 trace 上下文（如果有）
	ctx := generateParentSpanCtx(parentCtx, app.Annotations)

	stepStatus, progress, message := mapApplicationToApplyStep(app)

	logging.Logger.Info("Argo Application state changed",
		zap.String("app", app.Name),
		zap.String("namespace", app.Namespace),
		zap.String("jobID", jobID),
		zap.String("stepStatus", string(stepStatus)),
		zap.Int32("progress", progress),
		zap.String("sync", string(app.Status.Sync.Status)),
		zap.String("health", string(app.Status.Health.Status)),
	)

	updateJobApplyStep(ctx, jobID, app, message, stepStatus, progress)

	// ============================
	// 2️⃣ 终态打点（仅一次）
	// ============================

	if isTerminalStep(stepStatus) && isOperationFinished(app) {
		createApplicationSpan(ctx, app, stepStatus, message)
	}
}

// ============================
// 状态映射逻辑（关键）
// ============================

func mapApplicationToApplyStep(app *argov1alpha1.Application) (model.StepStatus, int32, string) {

	sync := app.Status.Sync.Status
	healthStatus := app.Status.Health.Status

	switch {
	case sync == argov1alpha1.SyncStatusCodeOutOfSync:
		return model.StepRunning, 30, "Application syncing"

	case sync == argov1alpha1.SyncStatusCodeSynced &&
		healthStatus == health.HealthStatusHealthy:
		return model.StepSucceeded, 100, "Application healthy"

	case healthStatus == health.HealthStatusDegraded ||
		healthStatus == health.HealthStatusMissing:
		return model.StepFailed, 100, "Application degraded"

	default:
		return model.StepRunning, 0, "Application progressing"
	}
}

// ============================
// Span 相关
// ============================

func createApplicationSpan(ctx context.Context, app *argov1alpha1.Application, status model.StepStatus, message string) {

	op := app.Status.OperationState
	if op == nil || op.FinishedAt == nil {
		return
	}
	annotations := app.GetAnnotations()
	if annotations == nil {
		return
	}

	traceIDStr := annotations[model.TraceIDAnnotation]
	parentSpanIDStr := annotations[model.SpanAnnotation]

	if traceIDStr == "" || parentSpanIDStr == "" {
		// 没有 trace 信息，不创建 span
		return
	}

	tracer := otel.Tracer("devflow-controller")

	startTime := op.StartedAt.Time
	endTime := op.FinishedAt.Time

	// 将 label 的 traceID/parentSpanID 转换成 OpenTelemetry SpanContext
	traceID, err := trace.TraceIDFromHex(traceIDStr)
	if err != nil {
		logging.Logger.Error("Invalid traceID", zap.String("traceID", traceIDStr), zap.Error(err))
		return
	}
	parentSpanID, err := trace.SpanIDFromHex(parentSpanIDStr)
	if err != nil {
		logging.Logger.Error("Invalid parentSpanID", zap.String("parentSpanID", parentSpanIDStr), zap.Error(err))
		return
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     parentSpanID,
		TraceFlags: trace.FlagsSampled,
	})

	parentCtx := trace.ContextWithRemoteSpanContext(ctx, sc)

	// 创建 span，从 startTime 到 endTime
	_, span := tracer.Start(parentCtx, app.Name, trace.WithTimestamp(startTime))
	span.SetAttributes(
		attribute.String("app", app.Name),
		attribute.String("namespace", app.Namespace),
		attribute.String("syncStatus", string(app.Status.Sync.Status)),
		attribute.String("healthStatus", string(app.Status.Health.Status)),
		attribute.String("stepStatus", string(status)),
		attribute.String("message", message),
	)

	logging.Logger.Info("application span created",
		zap.String("app", app.Name),
		zap.String("status", string(status)),
	)
	span.End(trace.WithTimestamp(endTime))
}

// ============================
// 工具函数
// ============================

func isTerminalStep(status model.StepStatus) bool {
	return status == model.StepSucceeded || status == model.StepFailed
}

func isOperationFinished(app *argov1alpha1.Application) bool {
	return app.Status.OperationState != nil &&
		app.Status.OperationState.FinishedAt != nil
}

func updateJobApplyStep(ctx context.Context, jobID string, app *argov1alpha1.Application, message string, stepStatus model.StepStatus, progress int32) {
	j, err := job.JobService.GetJobWithSteps(ctx, jobID)
	if err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "argo.application.get_job"),
			zap.String("jobID", jobID),
			zap.Error(err),
		)
		return
	}

	stepName := findApplyStepName(j.Steps)
	if stepName == "" {
		return
	}

	var start *time.Time
	var end *time.Time
	if app.Status.OperationState != nil {
		if !app.Status.OperationState.StartedAt.IsZero() {
			start = stepStartTime(j.Steps, stepName, app.Status.OperationState.StartedAt.Time)
		}
		if app.Status.OperationState.FinishedAt != nil && isTerminalStep(stepStatus) {
			end = stepEndTime(j.Steps, stepName, app.Status.OperationState.FinishedAt.Time)
		}
	}

	if err := job.JobService.UpdateJobStep(ctx, jobID, stepName, stepStatus, progress, message, start, end); err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "argo.application.apply_step_update"),
			zap.String("jobID", jobID),
			zap.String("stepName", stepName),
			zap.String("stepStatus", string(stepStatus)),
			zap.Int32("progress", progress),
			zap.Error(err),
		)
	}
}
