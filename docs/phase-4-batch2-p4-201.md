# Phase 4 Batch 2：P4-201 Fluxion Approval 五类终态与 request state machine

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：Approver / Fluxion
- Approver commit：`dae0e15` (`feat(integration): preserve Fluxion approval terminal decisions`)
- Fluxion commits：`b659f32` (`feat(approval): enforce terminal request state machine`), `8696902`
  (`fix(approval): guard duplicate result hashes`)
- 前置：[P4-200 rollout/drain](phase-4-batch2-p4-200.md)

## 终态契约

Approval result 的业务终态分为五类：

| 规范终态 | wire 值 | 语义 |
| --- | --- | --- |
| `APPROVED` | `APPROVED` | 审批通过，允许 Fluxion 继续派单 |
| `REJECTED` | `REJECTED` | 审批拒绝，走固定策略 fallback |
| `CANCELLED` | `CANCELLED` 或兼容 `WITHDRAWN` | 外部申请撤回/取消，不覆盖新事实 |
| `EXPIRED` | `EXPIRED` | 审批窗口到期，不无限等待 |
| `FAILED` | `FAILED` | 技术或集成失败，保留安全错误码，不能误放行 |

Approver 的 Fluxion result handler 现在对全部 wire decision 做严格透传；process terminal result `EXPIRED`
会发布为 `EXPIRED`，不再落入默认 `APPROVED` 分支。Fluxion 将 `WITHDRAWN` 规范化为持久化状态
`CANCELLED`，使 operator 查询只有一个取消状态。

## Fluxion request 状态机

```text
PENDING
  └──> APPROVED | REJECTED | CANCELLED | EXPIRED | FAILED

terminal
  └──> same decision + same decisionVersion = idempotent duplicate
  └──> different terminal decision/version = conflict (late result)
```

request row 的终态、decision version/hash、result Inbox 和 Temporal update outbox 在同一个数据库事务中
推进。并发或重复结果不会产生第二个可决定状态；`WITHDRAWN`/`CANCELLED` 的同版本重复结果也保持幂等。

## 验证

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| Fluxion state-machine unit test | PASS | 五类终态、`WITHDRAWN` alias、重复结果和冲突结果 |
| Fluxion `./gradlew --no-daemon test` | PASS | server 全量单测 |
| Approver `mvn -pl approver-application -am test` | PASS | 70 tests；result contract 六种 wire decision、`EXPIRED` publish 路径 |
| C-003/C-004/C-005/C-006 live E2E | SKIPPED | 需要 Temporal + PostgreSQL + Approver + Fluxion 的可重复隔离环境，本机未伪造 live PASS |

这项完成的是跨仓库契约与持久状态边界，不宣称五条真实 live 路径已经验收。下一项 P4-202 将处理
generation guard、晚到 result 和 reconciliation finding/operator view。
