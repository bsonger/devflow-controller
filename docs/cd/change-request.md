# CD 改造任务单（给 Codex 执行）

## 目标

让 `devflow-controller` 严格只做：

- 监听集群资源状态
- 同步 MongoDB 的 `steps`

不做：

- 修改任何 K8s 资源对象（含 label/status 回写）
- 业务状态决策推进

---

## 变更边界

仅允许修改：

- `pkg/controller/tekton.go`
- `pkg/controller/argo_cd.go`
- `pkg/controller/job_resources.go`
- `pkg/service/job/steps.go`
- `pkg/bootstrap/bootstrap.go`
- `docs/cd/_index.md`

禁止修改：

- 其他目录结构
- 外部依赖版本（`go.mod` / `go.sum`）

---

## 必做项

1. 删除所有对集群对象的写操作
   - 不再调用对 `Application` / `TaskRun` / `PipelineRun` 的 `Update`

2. 保留 informer 只读监听 + Mongo 同步
   - 仅更新 `manifest.steps` / `job.steps`

3. `job.steps` 缺失 step 时自动创建
   - 通过 `UpdateJobStep` 内部保证“无则创建”

4. 修复编译错误
   - `logging.InitZapLogger` 按 `devflow-common` 当前签名调用

---

## 验收标准

执行以下命令并通过：

1. `GOCACHE=/tmp/go-cache go test ./pkg/service/job`
2. `GOCACHE=/tmp/go-cache go test ./pkg/controller`
3. `GOCACHE=/tmp/go-cache go test ./...`

---

## 输出格式要求

Codex 最终输出必须包含：

1. 修改摘要（按文件列出）
2. 为什么这些改动符合“只同步 steps”
3. 测试命令与结果
4. 未处理风险（如有）
