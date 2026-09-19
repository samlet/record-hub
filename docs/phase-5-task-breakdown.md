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

Batch 3 收口记录：[phase-5-batch3-closure](phase-5-batch3-closure.md)。四项静态 contract 已通过；connector owner side-effect、Settlement relay/projection、故障注入、fallback/drain 和 rollback live 仍为 `SKIPPED`。

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-300 | Approver/Record Hub | connector registry、SDK semver、compatibility window | [closure](phase-5-batch3-p5-300.md)；exact registry/semver/fail-closed 静态 PASS，owner lifecycle live SKIPPED | PARTIAL |
| P5-301 | Approver/Settlement | Settlement confirmation request/result/Apply contract | [closure](phase-5-batch3-p5-301.md)；schema/fixture/hash 与 safe Apply 静态 PASS，owner Apply live SKIPPED | PARTIAL |
| P5-302 | Record Hub | Settlement safe association/projection | [closure](phase-5-batch3-p5-302.md)；safe model、版本/gap/conflict、scope/只读静态 PASS，live SKIPPED | PARTIAL |
| P5-303 | Fluxion/Bids | approval connector production hardening | [closure](phase-5-batch3-p5-303.md)；owner-side effect、reconciliation、fallback、rollback 静态 PASS，isolated owner fault/rollback live SKIPPED | PARTIAL |

## Batch 4：GA evidence 与治理

Batch 4 收口记录：[phase-5-batch4-closure](phase-5-batch4-closure.md)。四项静态验收门已通过；P4-501 prerequisite、runtime export、canary window、GA signoff 和 legacy review live 仍为 `SKIPPED`。

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-400 | 四仓库 | security/PII/sealed-data supply-chain scan | [closure](phase-5-batch4-p5-400.md)；当前树、safe projection、redaction/evidence contract 静态 PASS，runtime export live SKIPPED | PARTIAL |
| P5-401 | 四仓库 | single-tenant canary + full observation window | [closure](phase-5-batch4-p5-401.md)；scope、观察信号、停止/回滚 contract 静态 PASS，P4-501/live window SKIPPED | PARTIAL |
| P5-402 | Record Hub | Production GA report | [closure](phase-5-batch4-p5-402.md)；受控模板、RC/prerequisite 和字段校验静态 PASS，正式 GA evidence/signoff SKIPPED | PARTIAL |
| P5-403 | 四仓库 | fallback/legacy removal review | [closure](phase-5-batch4-p5-403.md)；KEEP 决策、drain/removal gate 和 destructive-action negative 静态 PASS，独立 live review SKIPPED | PARTIAL |

## Batch 5：GA release

Batch 5 preflight 收口记录：[phase-5-batch5-closure](phase-5-batch5-closure.md)。P5-500/P5-501 的静态 fail-closed 检查已通过，但 candidate 和 rollout 均明确阻塞。

| ID | 范围 | 任务 | 验收 | 状态 |
| --- | --- | --- | --- | --- |
| P5-500 | 四仓库 | GA candidate manifest | [closure](phase-5-batch5-p5-500.md)；fail-closed preflight 静态 PASS，当前 BLOCKED、禁止创建 candidate | BLOCKED_BY_GATES |
| P5-501 | 四仓库 | Production GA rollout | [closure](phase-5-batch5-p5-501.md)；fail-closed staged/rollback/audit preflight 静态 PASS，当前 BLOCKED、禁止 rollout | BLOCKED_BY_P5_500 |

## 执行规则

- `BLOCKED_BY_P4` 只能通过 P4-501 live gate 清除，不得由静态检查替代。
- 每个任务在涉及仓库独立提交；跨仓库 gate 只能引用实际 commit/artifact/evidence。
- 任何 scope 泄漏、敏感数据泄漏、重复领域副作用或不可逆 migration 立即停止后续 batch。
