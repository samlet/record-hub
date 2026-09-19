# Phase 5 任务分解：Production GA 与规模化

Phase 5 依赖 Phase 4 P4-501 live gate；在 live gate 未完成前只能进行设计、静态 contract 和环境准备，不能进入 GA 流量。

## Batch 0：入口与基线

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-000 | Record Hub | 方案、需求、验收和风险边界冻结 | 文档互链、non-goal 和 dependency 一致 | DONE |
| P5-001 | 四仓库 | RC/contract/migration/config baseline | commit、artifact、digest、owner 清单固定 | TODO |
| P5-002 | 四仓库 | Phase 4 live gap re-audit | P4-G1～G5 无 SKIPPED/PARTIAL | BLOCKED_BY_P4 |

## Batch 1：Production identity 与 control plane

Batch 1 收口记录：[phase-5-batch1-closure](phase-5-batch1-closure.md)。静态边界已完成；production live gate 仍按任务分别标记为 `SKIPPED`。

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-100 | Record Hub/Dex | production OIDC、workspace membership、service principal | [closure](phase-5-batch1-p5-100.md)；静态身份边界 PASS，production rotation/revocation live SKIPPED | PARTIAL |
| P5-101 | 四仓库 | tenant/org/connector policy lifecycle | [closure](phase-5-batch1-p5-101.md)；scope 静态矩阵 PASS，control-plane enable/disable/revoke live SKIPPED | PARTIAL |
| P5-102 | Record Hub | Viewer/Editor/Operator/Admin console/API parity | [closure](phase-5-batch1-p5-102.md)；Operator 专用运维权限与只读审批关联静态 PASS，角色 live SKIPPED | PARTIAL |

## Batch 2：HA data/event 与灾备

Batch 2 收口记录：[phase-5-batch2-closure](phase-5-batch2-closure.md)。四项静态 contract 已通过；HA/failover/restore/capacity/rollback live 仍需隔离生产-like topology。

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-200 | Record Hub | Mongo replica set、index、retention、PITR | [closure](phase-5-batch2-p5-200.md)；HA/index/retention/PITR 静态 contract PASS，failover/restore live SKIPPED | PARTIAL |
| P5-201 | Record Hub/四 owner | JetStream stream/consumer HA | [closure](phase-5-batch2-p5-201.md)；stream/consumer/ACK/DLQ/replay 静态 contract PASS，三节点故障/重启 live SKIPPED | PARTIAL |
| P5-202 | 四仓库 | capacity、SLO、RPO/RTO drill | [closure](phase-5-batch2-p5-202.md)；容量/SLO/RPO/RTO 静态 contract PASS，四 owner mixed-load live SKIPPED | PARTIAL |
| P5-203 | 四仓库 | production rotation/rolling rollback | [closure](phase-5-batch2-p5-203.md)；expand/contract、drain、rollback/no-double-write 静态 contract PASS，四 owner live SKIPPED | PARTIAL |

## Batch 3：Connector platform 与 Settlement

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-300 | Approver/Record Hub | connector registry、SDK semver、compatibility window | [closure](phase-5-batch3-p5-300.md)；exact registry/semver/fail-closed 静态 PASS，owner lifecycle live SKIPPED | PARTIAL |
| P5-301 | Approver/Settlement | Settlement confirmation request/result/Apply contract | [closure](phase-5-batch3-p5-301.md)；schema/fixture/hash 与 safe Apply 静态 PASS，owner Apply live SKIPPED | PARTIAL |
| P5-302 | Record Hub | Settlement safe association/projection | 不含金额/银行/附件/密封数据，late result/finding PASS | TODO |
| P5-303 | Fluxion/Bids | approval connector production hardening | owner-side effect、reconciliation、fallback、rollback PASS | TODO |

## Batch 4：GA evidence 与治理

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-400 | 四仓库 | security/PII/sealed-data supply-chain scan | Git/history/log/DLQ/metrics/evidence/artifact 无发现 | TODO |
| P5-401 | 四仓库 | single-tenant canary + full observation window | retention/retry/reconciliation/SLO 全部可接受 | TODO |
| P5-402 | Record Hub | Production GA report | commit/artifact/case/RPO/RTO/capacity/risk/rollback/signoff | TODO |
| P5-403 | 四仓库 | fallback/legacy removal review | 独立决策，未批准继续保留 | TODO |

## Batch 5：GA release

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-500 | 四仓库 | GA candidate manifest | all mandatory gates PASS、无继承 SKIPPED | TODO |
| P5-501 | 四仓库 | Production GA rollout | staged expansion、rollback window 和审计 PASS | TODO |

## 执行规则

- `BLOCKED_BY_P4` 只能通过 P4-501 live gate 清除，不得由静态检查替代。
- 每个任务在涉及仓库独立提交；跨仓库 gate 只能引用实际 commit/artifact/evidence。
- 任何 scope 泄漏、敏感数据泄漏、重复领域副作用或不可逆 migration 立即停止后续 batch。
