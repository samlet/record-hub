# Phase 4 Batch 2：P4-200 Fluxion tenant/workspace rollout 与在途 drain

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：Fluxion
- Fluxion commit：`4a6caf3` (`feat(approval): add scoped rollout flag and in-flight drain`)
- 任务入口：[phase-4-task-breakdown.md](phase-4-task-breakdown.md)

## 已实现的控制边界

Fluxion 的 external approval 配置现在有三层安全边界：

1. `enabled` 默认 `false`；没有完整 tenant/workspace 时，即使误设为 `true` 也不会创建外部 request。
2. `rolloutScopes` 是可选的逗号分隔精确 scope 列表，格式为 `tenant/workspace`。为空时只允许配置中的
   `tenantId/workspaceId`；非空时只允许列表中的 scope。未知 scope 不会因为全局开关打开而放行。
3. `ProjectService` 在启动 workflow 时把判定结果快照写入 Temporal input。关闭 flag 后新 workflow 使用本地
   human fallback；已经写入 history 的 workflow 不会因为配置重启而遗弃其 external request。

相关环境变量：

```text
FLUXION_EXTERNAL_APPROVAL_ENABLED=false
FLUXION_EXTERNAL_APPROVAL_TENANT_ID=<tenant>
FLUXION_EXTERNAL_APPROVAL_WORKSPACE_ID=<workspace>
FLUXION_EXTERNAL_APPROVAL_ROLLOUT_SCOPES=tenant-a/workspace-a,tenant-b/workspace-b
```

## 在途 drain 语义

关闭新流量只影响 workflow input 的新快照，不关闭已配置的持久化边界：

- Worker 继续运行 request relay，发送 flag rollback 前已经写入的 request outbox；不会创建新的 request，
  因为 emitter 已被关闭。
- API 只要保留 result token、tenant 和 workspace，就继续接收并幂等落库旧 request 的 result。
- Worker 继续运行 Temporal update relay，把已接收的结果应用到原 workflow；workflow 已完结或版本不匹配时，
  由既有冲突/幂等语义处理。

因此，停用方式是先将 `FLUXION_EXTERNAL_APPROVAL_ENABLED=false`，保留 request/result 凭据运行 drain；待
在途 request 完成或由 operator 显式取消后，才可撤销凭据。删除数据库 request/outbox 行不属于 flag rollback。

## 验证

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| `AppConfigTest` | PASS | 默认关闭、精确 scope、unknown scope、非法 scope 和 rollback 后 boundary 均覆盖 |
| `./gradlew --no-daemon test` | PASS | Fluxion server 全量单测 |
| C-001 flag/wrong tenant live | SKIPPED | 当前本机没有隔离的四 owner live topology；静态 config gate 已通过 |
| C-008 in-flight drain live | SKIPPED | 需要可重复的 Temporal + PostgreSQL + Approver result restart 窗口；未用单元测试冒充 live PASS |

本任务不宣称 Batch 2 或 Phase 4 Beta 完成。下一项 P4-201 需要在该 scope/drain 边界之上冻结
`APPROVED`、`REJECTED`、`CANCELLED/WITHDRAWN`、`EXPIRED`、`FAILED` 五类终态和跨仓库 request state machine。
