package controller

import (
	"context"
	"time"

	"github.com/bsonger/devflow-common/client/logging"
	"github.com/bsonger/devflow-common/client/tekton"
	"github.com/bsonger/devflow-common/model"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	informers "github.com/tektoncd/pipeline/pkg/client/informers/externalversions"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/apis"
	"reflect"

	"github.com/bsonger/devflow-controller/pkg/service/manifest"
)

func StartTektonInformer(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		tekton.TektonClient,
		0,
	)

	trInformer := factory.Tekton().V1().TaskRuns().Informer()

	trInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: onTaskRun,
		UpdateFunc: func(oldObj, newObj interface{}) {
			if reflect.DeepEqual(oldObj, newObj) {
				return // 对象没变化，直接返回
			}
			onTaskRun(newObj)
		},
	})

	go factory.Start(ctx.Done())

	// 等待缓存同步
	cache.WaitForCacheSync(ctx.Done(),
		trInformer.HasSynced,
	)

	return nil
}

func onTaskRun(obj interface{}) {
	tr, ok := obj.(*v1.TaskRun)
	if !ok || tr == nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "tekton.taskrun.type_assert"),
		)
		return
	}

	ctx := context.Background()
	ctx = generateParentSpanCtx(ctx, tr.Annotations)

	taskRefName := ""
	if tr.Spec.TaskRef != nil {
		taskRefName = tr.Spec.TaskRef.Name
	}

	logging.Logger.Debug("TaskRun event",
		zap.String("taskRun", tr.Name),
		zap.String("pipelineRun", tr.Labels["tekton.dev/pipelineRun"]),
		zap.String("pipelineTask", tr.Labels["tekton.dev/pipelineTask"]),
		zap.String("taskRef", taskRefName),
	)

	pipelineID := tr.Labels["tekton.dev/pipelineRun"]
	taskRun := tr.Name
	taskName := tr.Labels["tekton.dev/pipelineTask"]
	if pipelineID == "" || taskName == "" {
		logging.Logger.Warn(
			stepSyncFailedMsg,
			zap.String("stage", "tekton.taskrun.validate_labels"),
			zap.String("taskRun", taskRun),
			zap.String("pipelineRun", pipelineID),
			zap.String("pipelineTask", taskName),
		)
		return
	}

	if err := manifest.ManifestService.BindTaskRun(ctx, pipelineID, taskName, taskRun); err != nil {
		logging.Logger.Warn(
			stepSyncFailedMsg,
			zap.String("stage", "tekton.taskrun.bind"),
			zap.String("pipelineID", pipelineID),
			zap.String("taskName", taskName),
			zap.String("taskRun", taskRun),
			zap.Error(err),
		)
	}

	cond := tr.Status.GetCondition(apis.ConditionSucceeded)
	if cond == nil {
		return
	}

	switch cond.Status {
	case corev1.ConditionUnknown:
		var start *time.Time
		if tr.Status.StartTime != nil {
			t := tr.Status.StartTime.Time
			start = &t
		}
		if err := manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepRunning, cond.Message, start, nil); err != nil {
			logging.Logger.Warn(
				stepSyncFailedMsg,
				zap.String("stage", "tekton.taskrun.step_update"),
				zap.String("pipelineID", pipelineID),
				zap.String("taskName", taskName),
				zap.String("stepStatus", string(model.StepRunning)),
				zap.Error(err),
			)
		}

	case corev1.ConditionTrue:
		var end *time.Time
		if tr.Status.CompletionTime != nil {
			t := tr.Status.CompletionTime.Time
			end = &t
		}
		if err := manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepSucceeded, cond.Message, nil, end); err != nil {
			logging.Logger.Warn(
				stepSyncFailedMsg,
				zap.String("stage", "tekton.taskrun.step_update"),
				zap.String("pipelineID", pipelineID),
				zap.String("taskName", taskName),
				zap.String("stepStatus", string(model.StepSucceeded)),
				zap.Error(err),
			)
		}

	case corev1.ConditionFalse:
		var end *time.Time
		if tr.Status.CompletionTime != nil {
			t := tr.Status.CompletionTime.Time
			end = &t
		}
		if err := manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepFailed, cond.Message, nil, end); err != nil {
			logging.Logger.Warn(
				stepSyncFailedMsg,
				zap.String("stage", "tekton.taskrun.step_update"),
				zap.String("pipelineID", pipelineID),
				zap.String("taskName", taskName),
				zap.String("stepStatus", string(model.StepFailed)),
				zap.Error(err),
			)
		}
	}

	if tr.Status.StartTime != nil && tr.Status.CompletionTime != nil {
		createTaskRunSpan(ctx, tr)
	}
}

func createTaskRunSpan(ctx context.Context, tr *v1.TaskRun) {

	parentCtx := generateParentSpanCtx(ctx, tr.Annotations)

	startTime := tr.Status.StartTime.Time
	if tr.Status.CompletionTime == nil {
		// TaskRun 还没结束，不创建 span
		return
	}
	endTime := tr.Status.CompletionTime.Time

	tracer := otel.Tracer("devflow-controller")
	_, span := tracer.Start(parentCtx, tr.Name, trace.WithTimestamp(startTime))
	defer span.End(trace.WithTimestamp(endTime))

	// 可以添加一些 attribute
	span.SetAttributes(
		attribute.String("pipelineRun", tr.Labels["tekton.dev/pipelineRun"]),
		attribute.String("pipelineTask", tr.Labels["tekton.dev/pipelineTask"]),
	)
	logging.Logger.Info("create span complete", zap.String("taskRun", tr.Name))
}

func generateParentSpanCtx(ctx context.Context, annotations map[string]string) context.Context {
	if annotations == nil {
		return ctx
	}

	traceIDStr := annotations[model.TraceIDAnnotation]
	parentSpanIDStr := annotations[model.SpanAnnotation]
	if traceIDStr == "" || parentSpanIDStr == "" {
		// 没有 trace 信息，不创建 span
		return ctx
	}

	// 将 label 的 traceID/parentSpanID 转换成 OpenTelemetry SpanContext
	traceID, err := trace.TraceIDFromHex(traceIDStr)
	if err != nil {
		logging.Logger.Error("Invalid traceID", zap.String("traceID", traceIDStr), zap.Error(err))
		return ctx
	}
	parentSpanID, err := trace.SpanIDFromHex(parentSpanIDStr)
	if err != nil {
		logging.Logger.Error("Invalid parentSpanID", zap.String("parentSpanID", parentSpanIDStr), zap.Error(err))
		return ctx
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     parentSpanID,
		TraceFlags: trace.FlagsSampled,
	})
	parentCtx := trace.ContextWithRemoteSpanContext(ctx, sc)

	return parentCtx
}
