# DevFlow Controller 行为说明


本服务的唯一职责

监听集群资源状态，并将状态同步到 MongoDB 的 steps 字段中

目录结构参考
更新manifest 请参考 [reference/tekton.md]

更新job 请参考 [reference/argo.md]




## Controller 不做什么（非常重要）
- ❌ 不创建 PipelineRun / TaskRun
- ❌ 不修改 Application
- ❌ 不修改 Rollout
- ❌ 不生成发布流程
- ❌ 不做状态决策
- ❌ 不推进流程

