package controller

import (
	"context"
	"github.com/bsonger/devflow-common/client/logging"
	"go.uber.org/zap"
	"reflect"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	informers "github.com/tektoncd/pipeline/pkg/client/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/apis"

	"github.com/bsonger/devflow-common/client/tekton"
	"github.com/bsonger/devflow-common/model"

	"github.com/bsonger/devflow-controller/pkg/service/manifest"
)

func StartTektonInformer(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		tekton.TektonClient,
		0, // resync period
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = "status notin (Succeeded,Failed)"
		}),
	)

	prInformer := factory.Tekton().V1().PipelineRuns().Informer()
	trInformer := factory.Tekton().V1().TaskRuns().Informer()

	prInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: onPipelineRun,
		UpdateFunc: func(oldObj, newObj interface{}) {
			if oldObj == newObj {
				return
			}
			onPipelineRun(newObj)
		},
	})

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
		prInformer.HasSynced,
		trInformer.HasSynced,
	)

	return nil
}

func onTaskRun(obj interface{}) {
	tr := obj.(*v1.TaskRun)
	ctx := context.Background()
	logging.Logger.Debug("TaskRun event",
		zap.String("taskRun", tr.Name),
		zap.String("pipelineRun", tr.Labels["tekton.dev/pipelineRun"]),
		zap.String("pipelineTask", tr.Labels["tekton.dev/pipelineTask"]),
		zap.String("taskRef", tr.Spec.TaskRef.Name),
	)

	pipelineID := tr.Labels["tekton.dev/pipelineRun"]
	taskRun := tr.Name
	taskName := tr.Labels["tekton.dev/pipelineTask"]

	// 1️⃣ 检查 Label
	labels := tr.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if labels["status"] == "" {
		labels["status"] = "Running"
		tr.SetLabels(labels)
		_, err := tekton.TektonClient.TektonV1().TaskRuns(tr.Namespace).Update(ctx, tr, metav1.UpdateOptions{})
		if err != nil {
			logging.Logger.Error("Failed to set initial status label", zap.Error(err))
		}
	}

	// 1️⃣ 绑定 TaskRun
	_ = manifest.ManifestService.BindTaskRun(
		ctx, pipelineID, taskName, taskRun,
	)

	cond := tr.Status.GetCondition(apis.ConditionSucceeded)
	if cond == nil {
		return
	}

	var newStatusLabel string

	switch cond.Status {
	case corev1.ConditionUnknown:
		newStatusLabel = "Running"
		start := tr.Status.StartTime.Time
		_ = manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepRunning, cond.Message, &start, nil)

	case corev1.ConditionTrue:
		newStatusLabel = "Succeeded"
		end := tr.Status.CompletionTime.Time
		_ = manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepSucceeded, cond.Message, nil, &end)

	case corev1.ConditionFalse:

		newStatusLabel = "Failed"
		end := tr.Status.CompletionTime.Time
		_ = manifest.ManifestService.UpdateStepStatus(ctx, pipelineID, taskName, model.StepFailed, cond.Message, nil, &end)
	}

	// 4️⃣ 更新 TaskRun Label
	if labels["status"] != newStatusLabel {
		labels["status"] = newStatusLabel
		tr.SetLabels(labels)
		_, err := tekton.TektonClient.TektonV1().TaskRuns(tr.Namespace).Update(ctx, tr, metav1.UpdateOptions{})
		if err != nil {
			logging.Logger.Error("Failed to update TaskRun status label", zap.String("taskRun", tr.Name), zap.Error(err))
		}
	}
}

func onPipelineRun(obj interface{}) {
	pr := obj.(*v1.PipelineRun)
	ctx := context.Background()

	labels := pr.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	if labels["status"] == "" {
		labels["status"] = "Running"
		pr.SetLabels(labels)
		_, err := tekton.TektonClient.TektonV1().PipelineRuns(pr.Namespace).Update(ctx, pr, metav1.UpdateOptions{})
		if err != nil {
			logging.Logger.Error("Failed to set initial status label", zap.Error(err))
		}
	}

	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	if cond == nil {
		return
	}

	var newStatusLabel string
	var status model.ManifestStatus
	switch cond.Status {
	case corev1.ConditionUnknown:
		status = model.ManifestRunning
		newStatusLabel = "Running"
	case corev1.ConditionTrue:
		status = model.ManifestSucceeded
		newStatusLabel = "Succeeded"
	case corev1.ConditionFalse:
		status = model.ManifestFailed
		newStatusLabel = "Failed"
	}

	// 4️⃣ 更新 Manifest 状态
	_ = manifest.ManifestService.UpdateManifestStatus(ctx, pr.Name, status)

	// 5️⃣ 更新 Tekton 对象 Label
	if labels["status"] != newStatusLabel {
		labels["status"] = newStatusLabel
		pr.SetLabels(labels)
		_, err := tekton.TektonClient.TektonV1().PipelineRuns(pr.Namespace).Update(ctx, pr, metav1.UpdateOptions{})
		if err != nil {
			logging.Logger.Error("Failed to update status label", zap.String("pipelineRun", pr.Name), zap.Error(err))
		}
	}
}
