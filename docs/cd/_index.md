# CD Controller 文档

## 1) 服务职责（强约束）

DevFlow Controller 在 CD 场景的职责只有一件事：

- 监听集群资源状态
- 将状态同步到 MongoDB 的 `steps` 字段

明确不做：

- 不创建 `PipelineRun` / `TaskRun`
- 不修改 `Application`
- 不修改 `Rollout`
- 不生成发布流程
- 不做状态决策
- 不推进流程

---

## 2) 当前代码实际监听资源

根据当前实现，Controller 监听的资源如下：

- Tekton: `PipelineRun`、`TaskRun`
- Argo CD: `Application`
- Kubernetes: `Deployment`
- Argo Rollouts: `Rollout`

---

## 3) 当前状态同步行为

### Manifest 维度（Tekton）

- `TaskRun` 状态 → `manifest.steps[*].status/message/start_time/end_time`
- `PipelineRun` 状态 → `manifest.status`

### Job 维度（Argo + Deployment/Rollout）

- `Application` 事件驱动 Apply 类 step 更新
- `Deployment/Rollout` 事件驱动 Deploy/Canary/BlueGreen 类 step 更新
- 当 `job.steps` 中不存在目标 step 时，`UpdateJobStep` 会自动创建该 step

---

## 4) 代码现状与职责边界差距（建议改造）

以下是建议优先处理的差距：

1. `pkg/controller/tekton.go`
   - 目前会回写 `TaskRun/PipelineRun` 的 `status` label
   - 建议去掉对集群对象的写操作，仅做状态读取与 Mongo 同步

2. `pkg/controller/argo_cd.go`
   - 目前会回写 `Application` label（`status`）
   - 建议去掉对 `Application` 的写操作

3. `pkg/controller/argo_cd.go`
   - 目前包含 `mapApplicationToJobStatus` 等状态决策，并更新 `job.status`
   - 若严格遵守“只同步 steps”，建议将决策逻辑移出 Controller，仅保留 step 同步

4. `pkg/bootstrap/bootstrap.go`
   - `logging.InitZapLogger` 调用参数与 `devflow-common` 当前版本不一致
   - 需改为新签名，确保全项目可编译

---

## 5) 给 Codex 的操作方式（下次直接改文档即可）

请把每次需求写到：`docs/cd/change-request.md`。

推荐提示词：

`请按 docs/cd/change-request.md 执行，只改文档中明确列出的文件，并跑最小验证。`

这样你只需要维护文档，Codex 可以按文档中的边界和验收项执行。
