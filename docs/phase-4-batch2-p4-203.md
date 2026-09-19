# Phase 4 Batch 2：P4-203 local human fallback 与 replay/Continue-As-New

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：Fluxion
- 前置：[P4-200 rollout/drain](phase-4-batch2-p4-200.md)、[P4-201 state machine](phase-4-batch2-p4-201.md)、[P4-202 reconciliation](phase-4-batch2-p4-202.md)
- Fluxion commit：`bf538f6`（`test(approval): prove replay and continue-as-new safety`）

## 交付边界

- `externalApprovalEnabled=false` 时，低置信度分支继续创建 Fluxion 本地 HumanTask，不向 Approver 发送外部申请。
- 外部路径的 request ID 由 `projectId|tradeId|dispatchGeneration` 稳定派生；Temporal Activity retry、workflow replay 和
  Continue-As-New 使用同一 generation 时复用原 request，不重新创建 Application 或 update outbox。
- Continue-As-New 保留 `externalApprovalEnabled` 和 workflow state；新 run ID 只属于 Temporal execution，不参与外部
  Application 幂等键。

## 验证

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| Fluxion local human fallback workflow tests | PASS | flag off 的低置信度路径创建本地人工任务并等待决定 |
| Fluxion stable emitter/retry test | PASS | Activity 重试不重复 request/outbox，新的 workflow run ID 仍返回原 request |
| Fluxion Continue-As-New bounded-history tests | PASS | state/flag 携带到下一 run，历史保持有界 |
| Temporal + Approver live replay/CAN | SKIPPED | 当前没有隔离 worker、Temporal server、PostgreSQL 和 Approver live fixture |

该项完成的是 replay-safe contract 和本地 fallback 证明；完整跨服务 live gate 仍需隔离环境。
