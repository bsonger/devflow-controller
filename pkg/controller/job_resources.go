package controller

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	rolloutv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	"github.com/bsonger/devflow-common/client/argo"
	"github.com/bsonger/devflow-common/client/logging"
	"github.com/bsonger/devflow-common/model"
	"github.com/bsonger/devflow-controller/pkg/config"
	"github.com/bsonger/devflow-controller/pkg/service/job"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var jobIDCache = struct {
	sync.RWMutex
	byApp map[string]string
}{
	byApp: map[string]string{},
}

func StartJobResourceInformer(ctx context.Context) error {
	kubeCfg, err := config.LoadKubeConfig()
	if err != nil {
		return err
	}

	kubeClient, err := kubernetes.NewForConfig(kubeCfg)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(kubeCfg)
	if err != nil {
		return err
	}

	deployFactory := kubeinformers.NewSharedInformerFactory(kubeClient, 0)
	deployInformer := deployFactory.Apps().V1().Deployments().Informer()
	deployInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: onDeployment,
		UpdateFunc: func(oldObj, newObj interface{}) {
			if oldObj == newObj {
				return
			}
			onDeployment(newObj)
		},
	})

	rolloutGVR := schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "rollouts",
	}
	rolloutFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 0, metav1.NamespaceAll, nil)
	rolloutInformer := rolloutFactory.ForResource(rolloutGVR).Informer()
	rolloutInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: onRollout,
		UpdateFunc: func(oldObj, newObj interface{}) {
			if oldObj == newObj {
				return
			}
			onRollout(newObj)
		},
	})

	go deployFactory.Start(ctx.Done())
	go rolloutFactory.Start(ctx.Done())

	cache.WaitForCacheSync(ctx.Done(),
		deployInformer.HasSynced,
		rolloutInformer.HasSynced,
	)

	return nil
}

func onDeployment(obj interface{}) {
	dep, ok := obj.(*appsv1.Deployment)
	if !ok {
		return
	}

	ctx := context.Background()
	jobID := resolveJobID(ctx, dep.Labels)
	if jobID == "" {
		return
	}

	j, err := job.JobService.GetJobWithSteps(ctx, jobID)
	if err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "deployment.get_job"),
			zap.String("jobID", jobID),
			zap.Error(err),
		)
		return
	}

	applyStepName := findApplyStepName(j.Steps)
	if applyStepName != "" && !isStepSucceeded(j.Steps, applyStepName) {
		return
	}

	deployStepName := findDeployStepName(j.Steps, applyStepName)
	if deployStepName == "" {
		return
	}

	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	ready := dep.Status.ReadyReplicas
	if ready == 0 {
		ready = dep.Status.AvailableReplicas
	}
	if ready == 0 {
		ready = dep.Status.UpdatedReplicas
	}

	progress := percent(ready, desired)
	status := model.StepRunning
	message := fmt.Sprintf("pods ready %d/%d", ready, desired)

	start := stepStartTime(j.Steps, deployStepName, dep.CreationTimestamp.Time)
	var end *time.Time
	if desired > 0 && ready >= desired {
		status = model.StepSucceeded
		progress = 100
		now := time.Now()
		end = stepEndTime(j.Steps, deployStepName, now)
	}

	if err := job.JobService.UpdateJobStep(ctx, jobID, deployStepName, status, progress, message, start, end); err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "deployment.step_update"),
			zap.String("jobID", jobID),
			zap.String("stepName", deployStepName),
			zap.String("stepStatus", string(status)),
			zap.Int32("progress", progress),
			zap.Error(err),
		)
	}
}

func onRollout(obj interface{}) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	var rollout rolloutv1alpha1.Rollout
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &rollout); err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "rollout.convert"),
			zap.Error(err),
		)
		return
	}

	ctx := context.Background()
	jobID := resolveJobID(ctx, rollout.Labels)
	if jobID == "" {
		return
	}

	j, err := job.JobService.GetJobWithSteps(ctx, jobID)
	if err != nil {
		logging.Logger.Warn(stepSyncFailedMsg,
			zap.String("stage", "rollout.get_job"),
			zap.String("jobID", jobID),
			zap.Error(err),
		)
		return
	}

	applyStepName := findApplyStepName(j.Steps)
	if applyStepName != "" && !isStepSucceeded(j.Steps, applyStepName) {
		return
	}

	switch {
	case rollout.Spec.Strategy.Canary != nil:
		handleCanaryRollout(ctx, j, &rollout)
	case rollout.Spec.Strategy.BlueGreen != nil:
		handleBlueGreenRollout(ctx, j, &rollout)
	default:
	}
}

func handleCanaryRollout(ctx context.Context, j *job.JobWithSteps, rollout *rolloutv1alpha1.Rollout) {
	weight := rolloutCanaryWeight(rollout)

	targets := canaryTargetsFromSteps(j.Steps)
	if len(targets) == 0 {
		return
	}

	current := int(weight)
	prevTarget := 0
	nextIndex := -1

	for i, step := range targets {
		if current >= step.Target {
			start := stepStartTime(j.Steps, step.Name, rollout.CreationTimestamp.Time)
			now := time.Now()
			end := stepEndTime(j.Steps, step.Name, now)
			message := fmt.Sprintf("canary weight %d%% (target %d%%)", current, step.Target)
			if err := job.JobService.UpdateJobStep(ctx, j.ID.Hex(), step.Name, model.StepSucceeded, 100, message, start, end); err != nil {
				logging.Logger.Warn(
					stepSyncFailedMsg,
					zap.String("stage", "rollout.canary.step_update"),
					zap.String("jobID", j.ID.Hex()),
					zap.String("stepName", step.Name),
					zap.Int("target", step.Target),
					zap.Int("current", current),
					zap.String("stepStatus", string(model.StepSucceeded)),
					zap.Error(err),
				)
			}
			prevTarget = step.Target
			continue
		}
		if nextIndex == -1 {
			nextIndex = i
		}
	}

	if nextIndex == -1 {
		return
	}

	step := targets[nextIndex]
	progress := segmentPercent(current, prevTarget, step.Target)
	start := stepStartTime(j.Steps, step.Name, rollout.CreationTimestamp.Time)
	message := fmt.Sprintf("canary weight %d%% (target %d%%)", current, step.Target)
	if err := job.JobService.UpdateJobStep(ctx, j.ID.Hex(), step.Name, model.StepRunning, progress, message, start, nil); err != nil {
		logging.Logger.Warn(
			stepSyncFailedMsg,
			zap.String("stage", "rollout.canary.step_update"),
			zap.String("jobID", j.ID.Hex()),
			zap.String("stepName", step.Name),
			zap.Int("target", step.Target),
			zap.Int("current", current),
			zap.Int32("progress", progress),
			zap.String("stepStatus", string(model.StepRunning)),
			zap.Error(err),
		)
	}
}

func handleBlueGreenRollout(ctx context.Context, j *job.JobWithSteps, rollout *rolloutv1alpha1.Rollout) {
	greenStepName, trafficStepName := findBlueGreenStepNames(j.Steps)
	if greenStepName == "" && trafficStepName == "" {
		return
	}

	desired := int32(1)
	if rollout.Spec.Replicas != nil {
		desired = *rollout.Spec.Replicas
	}

	updated := rollout.Status.UpdatedReplicas
	progress := percent(updated, desired)
	message := fmt.Sprintf("green pods ready %d/%d", updated, desired)

	start := stepStartTime(j.Steps, greenStepName, rollout.CreationTimestamp.Time)
	greenStatus := model.StepRunning
	var greenEnd *time.Time
	if desired > 0 && updated >= desired {
		greenStatus = model.StepSucceeded
		progress = 100
		now := time.Now()
		greenEnd = stepEndTime(j.Steps, greenStepName, now)
	}

	if greenStepName != "" {
		if err := job.JobService.UpdateJobStep(ctx, j.ID.Hex(), greenStepName, greenStatus, progress, message, start, greenEnd); err != nil {
			logging.Logger.Warn(
				stepSyncFailedMsg,
				zap.String("stage", "rollout.bluegreen.green_step_update"),
				zap.String("jobID", j.ID.Hex()),
				zap.String("stepName", greenStepName),
				zap.Int32("updated", updated),
				zap.Int32("desired", desired),
				zap.Int32("progress", progress),
				zap.String("stepStatus", string(greenStatus)),
				zap.Error(err),
			)
		}
	}

	if trafficStepName == "" {
		return
	}

	if greenStatus != model.StepSucceeded {
		return
	}

	promoted := blueGreenPromoted(rollout)
	trafficStatus := model.StepRunning
	trafficProgress := int32(0)
	trafficMessage := "waiting for traffic switch"
	var trafficEnd *time.Time
	if promoted {
		trafficStatus = model.StepSucceeded
		trafficProgress = 100
		trafficMessage = "traffic switched to green"
		now := time.Now()
		trafficEnd = stepEndTime(j.Steps, trafficStepName, now)
	}

	trafficStart := stepStartTime(j.Steps, trafficStepName, rollout.CreationTimestamp.Time)
	if err := job.JobService.UpdateJobStep(ctx, j.ID.Hex(), trafficStepName, trafficStatus, trafficProgress, trafficMessage, trafficStart, trafficEnd); err != nil {
		logging.Logger.Warn(
			stepSyncFailedMsg,
			zap.String("stage", "rollout.bluegreen.traffic_step_update"),
			zap.String("jobID", j.ID.Hex()),
			zap.String("stepName", trafficStepName),
			zap.Bool("promoted", promoted),
			zap.Int32("progress", trafficProgress),
			zap.String("stepStatus", string(trafficStatus)),
			zap.Error(err),
		)
	}
}

func blueGreenPromoted(rollout *rolloutv1alpha1.Rollout) bool {
	bg := rolloutBlueGreenStatus(rollout)
	if bg == nil {
		return false
	}
	if bg.ActiveSelector != "" && bg.PreviewSelector != "" && bg.ActiveSelector == bg.PreviewSelector {
		return true
	}
	if rollout.Status.Phase == rolloutv1alpha1.RolloutPhaseHealthy && rollout.Status.UpdatedReplicas > 0 {
		return true
	}
	return false
}

func rolloutCanaryWeight(rollout *rolloutv1alpha1.Rollout) int32 {
	if rollout == nil {
		return 0
	}

	if w, ok := findInt32Field(reflect.ValueOf(&rollout.Status), "CurrentWeight", "CanaryWeight", "TrafficWeight", "CurrentTrafficWeight"); ok {
		return w
	}

	if w, ok := findInt32Field(reflect.ValueOf(rollout.Status.Canary), "CurrentWeight", "CanaryWeight", "TrafficWeight", "CurrentTrafficWeight"); ok {
		return w
	}

	if w, ok := findCanaryWeightInNested(reflect.ValueOf(rollout.Status.Canary)); ok {
		return w
	}

	return 0
}

func rolloutBlueGreenStatus(rollout *rolloutv1alpha1.Rollout) *rolloutv1alpha1.BlueGreenStatus {
	if rollout == nil {
		return nil
	}
	switch bg := any(rollout.Status.BlueGreen).(type) {
	case *rolloutv1alpha1.BlueGreenStatus:
		return bg
	case rolloutv1alpha1.BlueGreenStatus:
		return &bg
	default:
		return nil
	}
}

func findCanaryWeightInNested(v reflect.Value) (int32, bool) {
	v = derefValue(v)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return 0, false
	}

	weights := v.FieldByName("Weights")
	weights = derefValue(weights)
	if weights.IsValid() && weights.Kind() == reflect.Struct {
		canary := weights.FieldByName("Canary")
		canary = derefValue(canary)
		if canary.IsValid() && canary.Kind() == reflect.Struct {
			if w, ok := findInt32Field(canary, "Weight"); ok {
				return w, true
			}
		}
		if w, ok := findInt32Field(weights, "Weight"); ok {
			return w, true
		}
	}

	return 0, false
}

func findInt32Field(v reflect.Value, names ...string) (int32, bool) {
	v = derefValue(v)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return 0, false
	}
	for _, name := range names {
		f := v.FieldByName(name)
		if !f.IsValid() {
			continue
		}
		f = derefValue(f)
		switch f.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int32(f.Int()), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int32(f.Uint()), true
		}
	}
	return 0, false
}

func derefValue(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func cacheJobID(appName, jobID string) {
	if appName == "" || jobID == "" {
		return
	}
	jobIDCache.Lock()
	jobIDCache.byApp[appName] = jobID
	jobIDCache.Unlock()
}

func resolveJobID(ctx context.Context, labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if jobID := labels[model.JobIDLabel]; jobID != "" {
		return jobID
	}
	if jobID := labels["devflow/job-id"]; jobID != "" {
		return jobID
	}

	appName := appNameFromLabels(labels)
	if appName == "" {
		return ""
	}

	jobIDCache.RLock()
	if jobID, ok := jobIDCache.byApp[appName]; ok {
		jobIDCache.RUnlock()
		return jobID
	}
	jobIDCache.RUnlock()

	app, err := argo.ArgoCdClient.ArgoprojV1alpha1().Applications("argocd").Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	jobID := app.Labels[model.JobIDLabel]
	cacheJobID(appName, jobID)
	return jobID
}

func appNameFromLabels(labels map[string]string) string {
	for _, key := range []string{
		"argocd.argoproj.io/instance",
		"app.kubernetes.io/instance",
		"app",
	} {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

func findApplyStepName(steps []job.JobStep) string {
	for _, step := range steps {
		if containsAny(step.Name, "apply", "sync", "manifest", "config", "部署", "配置", "同步") {
			return step.Name
		}
	}
	if len(steps) > 0 {
		return steps[0].Name
	}
	return ""
}

func findDeployStepName(steps []job.JobStep, applyStepName string) string {
	for _, step := range steps {
		if step.Name == applyStepName {
			continue
		}
		if containsAny(step.Name, "deploy", "rollout", "pod", "ready", "deployment", "发布", "启动") {
			return step.Name
		}
	}
	for _, step := range steps {
		if step.Name != applyStepName {
			return step.Name
		}
	}
	return ""
}

func findBlueGreenStepNames(steps []job.JobStep) (string, string) {
	var greenStep, trafficStep string
	for _, step := range steps {
		if greenStep == "" && containsAny(step.Name, "green", "preview", "新版本", "新环境", "预览", "启动", "ready") {
			greenStep = step.Name
			continue
		}
		if trafficStep == "" && containsAny(step.Name, "traffic", "switch", "promote", "active", "cutover", "切流量", "流量", "上线") {
			trafficStep = step.Name
		}
	}

	if greenStep == "" && len(steps) > 0 {
		greenStep = steps[0].Name
	}
	if trafficStep == "" {
		for _, step := range steps {
			if step.Name != greenStep {
				trafficStep = step.Name
				break
			}
		}
	}
	return greenStep, trafficStep
}

func canaryTargetsFromSteps(steps []job.JobStep) []canaryTarget {
	var targets []canaryTarget
	for _, step := range steps {
		if target, ok := extractPercent(step.Name); ok {
			if target > 100 {
				continue
			}
			targets = append(targets, canaryTarget{
				Name:   step.Name,
				Target: target,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Target < targets[j].Target
	})
	return targets
}

type canaryTarget struct {
	Name   string
	Target int
}

var percentRegex = regexp.MustCompile(`(\d{1,3})%`)

func extractPercent(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	if match := percentRegex.FindStringSubmatch(name); len(match) == 2 {
		return atoi(match[1]), true
	}
	if containsAny(name, "traffic", "流量", "canary") {
		digits := regexp.MustCompile(`\d{1,3}`).FindString(name)
		if digits != "" {
			return atoi(digits), true
		}
	}
	return 0, false
}

func isStepSucceeded(steps []job.JobStep, name string) bool {
	for _, step := range steps {
		if step.Name == name {
			return step.Status == model.StepSucceeded
		}
	}
	return false
}

func stepStartTime(steps []job.JobStep, name string, fallback time.Time) *time.Time {
	for _, step := range steps {
		if step.Name == name {
			if step.StartTime != nil {
				return nil
			}
			t := fallback
			return &t
		}
	}
	return nil
}

func stepEndTime(steps []job.JobStep, name string, fallback time.Time) *time.Time {
	for _, step := range steps {
		if step.Name == name {
			if step.EndTime != nil {
				return nil
			}
			t := fallback
			return &t
		}
	}
	return nil
}

func percent(current, total int32) int32 {
	if total <= 0 {
		return 0
	}
	p := int32(math.Round(float64(current) / float64(total) * 100))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func segmentPercent(current, prev, target int) int32 {
	if target <= prev {
		return 100
	}
	if current <= prev {
		return 0
	}
	if current >= target {
		return 100
	}
	return int32(math.Round(float64(current-prev) / float64(target-prev) * 100))
}

func containsAny(value string, needles ...string) bool {
	if value == "" {
		return false
	}
	low := strings.ToLower(value)
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(low, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			continue
		}
		n = n*10 + int(r-'0')
	}
	return n
}
