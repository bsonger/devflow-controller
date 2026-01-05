package controller

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"reflect"

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	JobIDAnnotation = "devflow.io/job-id"
)

// ============================
// Informer 启动入口
// ============================

func StartArgoApplicationInformer(ctx context.Context) error {

	factory := argoinformers.NewSharedInformerFactoryWithOptions(
		argo.ArgoCdClient,
		0,
		argoinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			// 只监听 DevFlow 创建的 Application
			options.LabelSelector = "status notin (Succeeded,Failed)"
		}),
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
	app := obj.(*argov1alpha1.Application)

	jobID := app.Labels[model.JobIDLabel]
	if jobID == "" {
		return
	}

	// 生成 trace 上下文（如果有）
	ctx := generateParentSpanCtx(parentCtx, app.Annotations)

	jobStatus, message := mapApplicationToJobStatus(app)

	logging.Logger.Info("Argo Application state changed",
		zap.String("app", app.Name),
		zap.String("namespace", app.Namespace),
		zap.String("jobID", jobID),
		zap.String("jobStatus", string(jobStatus)),
		zap.String("sync", string(app.Status.Sync.Status)),
		zap.String("health", string(app.Status.Health.Status)),
	)

	// ============================
	// 1️⃣ 更新 Job 状态（幂等）
	// ============================

	if err := job.JobService.UpdateJobStatus(ctx, jobID, jobStatus); err != nil {
		logging.Logger.Error("update job status failed",
			zap.String("jobID", jobID),
			zap.Error(err),
		)
		return
	}

	// ============================
	// 2️⃣ 终态打点（仅一次）
	// ============================

	if isTerminalJob(jobStatus) && isOperationFinished(app) {
		createApplicationSpan(ctx, app, jobStatus, message)
		app.Labels["status"] = string(jobStatus)
		_, err := argo.ArgoCdClient.ArgoprojV1alpha1().Applications("argocd").Update(ctx, app, metav1.UpdateOptions{})
		if err != nil {
			logging.Logger.Error("update application failed")
		}
	}
}

// ============================
// 状态映射逻辑（关键）
// ============================

func mapApplicationToJobStatus(app *argov1alpha1.Application) (model.JobStatus, string) {

	sync := app.Status.Sync.Status
	healthStatus := app.Status.Health.Status

	switch {
	case sync == argov1alpha1.SyncStatusCodeOutOfSync:
		return model.JobRunning, "Application syncing"

	case sync == argov1alpha1.SyncStatusCodeSynced &&
		healthStatus == health.HealthStatusHealthy:
		return model.JobSucceeded, "Application healthy"

	case healthStatus == health.HealthStatusDegraded ||
		healthStatus == health.HealthStatusMissing:
		return model.JobFailed, "Application degraded"

	default:
		return model.JobRunning, "Application progressing"
	}
}

// ============================
// Span 相关
// ============================

func createApplicationSpan(ctx context.Context, app *argov1alpha1.Application, status model.JobStatus, message string) {

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
		attribute.String("jobStatus", string(status)),
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

func isTerminalJob(status model.JobStatus) bool {
	return status == model.JobSucceeded || status == model.JobFailed
}

func isOperationFinished(app *argov1alpha1.Application) bool {
	return app.Status.OperationState != nil &&
		app.Status.OperationState.FinishedAt != nil
}
