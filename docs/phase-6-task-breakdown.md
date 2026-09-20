# Phase 6 任务分解：真实业务扩展与自助化平台

Phase 6 只在 Phase 5 GA 前置完成后进入 live implementation；当前先冻结方案和任务边界。

## Batch 0：入口与真实 contract inventory

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-000 | 四仓库 | 依赖与 owner 清单 | Phase 5 PASS、owner signoff、真实 topology 前置 | BLOCKED_BY_P5 |
| P6-001 | 四仓库 | connector/schema inventory | [inventory](phase-6-connector-schema-inventory.json)；113 项 source asset、owner/schemaVersion/SHA-256 矩阵已冻结 | DONE |
| P6-002 | Record Hub | Phase 6 evidence contract | [evidence contract](phase-6-evidence-contract.json)；13 项必需字段、redaction、rollback、audit 模板已冻结 | DONE |

## Batch 1：多维表格增强

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-100 | Record Hub | schema.org 语义和继承 registry | IRI allowlist、版本、兼容检查；[registry contract](phase-6-schema-registry.json) 已通过静态 gate | IN_PROGRESS |
| P6-101 | Record Hub | tags、tag dictionary 和 audit | 受控 tag、互斥组、scope API；[tag contract](phase-6-tags-contract.json) 已冻结 | DONE |
| P6-102 | Record Hub | typed relation 和安全摘要 | cardinality、source version、hash、gap/finding；[relation contract](phase-6-relations-contract.json) 已通过静态 gate | IN_PROGRESS |
| P6-103 | Record Hub/Web | views、查询 cost 和 redacted export | pagination、sort、rate/backpressure | TODO |

## Batch 2：Connector SDK 与 registry

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-200 | Record Hub | connector registry lifecycle | exact match、enable/disable、owner、compatibility | TODO |
| P6-201 | 四仓库 | JVM/Go/TypeScript SDK parity | typed ref、command/result、receipt、errors | TODO |
| P6-202 | 四仓库 | workload identity lifecycle | independent principal、rotation、drain、revoke | TODO |
| P6-203 | 四仓库 | connector fault matrix | duplicate、late result、restart、DLQ、rollback | TODO |

## Batch 3：Approver、Fluxion、Bids 真实切片

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-300 | Approver/Record Hub | Application/Process 安全投影 | allowlist schema、association、reconciliation | TODO |
| P6-301 | Fluxion | 低置信度派单审批 connector | tenant rollout、local fallback、Temporal update receipt | TODO |
| P6-302 | Bids | 招标准备发布审批 connector | safe metadata、Conductor task completion、drain | TODO |
| P6-303 | Approver/Settlement | Settlement confirmation safe association | request/result/Apply boundary、redaction | TODO |

## Batch 4：Workflow binding 与 event bus

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-400 | Temporal | typed snapshot/ref Activity adapter | deterministic history、retry、hash assertion | TODO |
| P6-401 | Conductor | task input/output adapter | receipt polling、timeout、replay | TODO |
| P6-402 | NATS | registry-backed event/replay API | subject ACL、durable、DLQ、backpressure | TODO |
| P6-403 | Record Hub | event-to-table state mapping | source pointer、gap/conflict、rebuild | TODO |

## Batch 5：自助化 control plane 与运营

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-500 | Web/API | table/schema/tag/relation console | RBAC、audit、safe export | TODO |
| P6-501 | Web/API | connector onboarding workflow | contract upload、review、compatibility、approval | TODO |
| P6-502 | Ops | observation/reconciliation dashboard | lag/backlog/retry/dead/finding/recovery | TODO |
| P6-503 | Ops | operator runbook and rollback | stop/drain/replay/disable/rollback evidence | TODO |

## Batch 6：规模与多地域评估

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-600 | 四仓库 | production-like mixed load | cost、capacity、backpressure、SLO | TODO |
| P6-601 | 数据层 | archive/retention/restore extension | PITR、archive、rebuild、retention proof | TODO |
| P6-602 | 架构 | multi-region decision record | active-active feasibility、RPO/RTO、成本和 go/no-go | TODO |

## 执行约束

- P6-000 未解除前只做文档、contract 和 fixture，不启动真实新 connector 流量。
- 每项跨仓库任务必须在各仓库独立提交，并把实际 commit/artifact/evidence 写入 manifest。
- 任何 `SKIPPED`、`PARTIAL`、`UNVERIFIED` 或敏感字段泄漏都阻止下一批 live work。
