# Phase 4 Batch 2：P4-202 late result、generation guard 与 reconciliation

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：Fluxion / Approver
- 前置：[P4-201 五类终态](phase-4-batch2-p4-201.md)
- Fluxion commit：`74fd10b`（`feat(approval): guard late generation results`）
- Approver commit：`a094cca`（`feat(reconciliation): add version drift findings`）

## 交付边界

Fluxion 以 `(tenantId, workspaceId, projectRef, dispatchGeneration)` 选择最新申请。晚到的旧 generation
结果只写入 Result Inbox，并生成 `VERSION_DRIFT` reconciliation event；它不会创建 Temporal update outbox，也不会
覆盖当前 generation 的终态。相同 generation 的重复结果仍由 P4-201 state machine 以 decision version/hash
幂等处理，冲突结果保持安全失败。

Approver reconciliation 将本地结果版本与远端 Fluxion decision version 对齐检查，暴露
`VERSION_DRIFT` 和远端版本落后时的 `LATE_RESULT` finding。远端 `CANCELLED`、`EXPIRED`、`FAILED` 映射到
相同语义的 operator 状态，避免把异常结果误显示为未执行或批准。

## 验证

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| Fluxion late-generation/result Inbox unit test | PASS | 旧 generation 记录 Inbox/reconciliation，不产生 Temporal update |
| Fluxion state-machine/full server tests | PASS | 五类终态、duplicate/hash guard 与 generation guard |
| Approver reconciliation/application tests | PASS | VERSION_DRIFT/LATE_RESULT finding、终态映射和 admin result version |
| P4-202 static contract review | PASS | finding taxonomy、safe operator fields、tenant/workspace scope |
| C-007 late result + cross-service live topology | SKIPPED | 需要隔离 Temporal、PostgreSQL、NATS、Approver、Fluxion 实例；当前本机未伪造 live PASS |

本项不改变新事实，不会将晚到结果重新应用到 Temporal；live gate 保留到具备隔离拓扑后重跑。
