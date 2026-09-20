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
| P6-100 | Record Hub | schema.org 语义和继承 registry | IRI allowlist、版本、兼容检查；[registry contract](phase-6-schema-registry.json) 已通过静态 gate | DONE |
| P6-101 | Record Hub | tags、tag dictionary 和 audit | 受控 tag、互斥组、scope API；[tag contract](phase-6-tags-contract.json) 已冻结 | DONE |
| P6-102 | Record Hub | typed relation 和安全摘要 | cardinality、source version、hash、gap/finding；[relation contract](phase-6-relations-contract.json) 已冻结 | DONE |
| P6-103 | Record Hub/Web | views、查询 cost 和 redacted export | pagination、sort、rate/backpressure；[view contract](phase-6-views-contract.json) 已冻结 | DONE |

## Batch 2：Connector SDK 与 registry

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-200 | Record Hub | connector registry lifecycle | exact match、enable/disable、owner、compatibility；[registry contract](phase-6-connector-registry.json) 已冻结 | DONE |
| P6-201 | 四仓库 | JVM/Go/TypeScript SDK parity | typed ref、command/result、receipt、errors；[parity inventory](phase-6-sdk-parity.json) 已盘点，存在 SDK 缺口 | BLOCKED_BY_SDK_GAP |
| P6-202 | 四仓库 | workload identity lifecycle | independent principal、rotation、drain、revoke；[identity audit](phase-6-workload-identity.json) 显示 rotation 状态机缺口 | BLOCKED_BY_IDENTITY_LIFECYCLE_GAP |
| P6-203 | 四仓库 | connector fault matrix | duplicate、late result、restart、DLQ、rollback；[fault matrix](phase-6-fault-matrix.json) 缺 immutable live evidence | BLOCKED_BY_LIVE_FAULT_EVIDENCE |

## Batch 3：Approver、Fluxion、Bids 真实切片

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-300 | Approver/Record Hub | Application/Process 安全投影 | allowlist schema、association、reconciliation；[Approver projection](phase-6-approver-projection.json) 缺 native live evidence | BLOCKED_BY_LIVE_CONNECTOR |
| P6-301 | Fluxion | 低置信度派单审批 connector | tenant rollout、local fallback、Temporal update receipt；[Fluxion approval](phase-6-fluxion-approval.json) 缺 native live evidence | BLOCKED_BY_LIVE_CONNECTOR |
| P6-302 | Bids | 招标准备发布审批 connector | safe metadata、Conductor task completion、drain；[Bids approval](phase-6-bids-approval.json) 缺 native live evidence | BLOCKED_BY_LIVE_CONNECTOR |
| P6-303 | Approver/Settlement | Settlement confirmation safe association | request/result/Apply boundary、redaction；[Settlement association](phase-6-settlement-association.json) 缺 native live evidence | BLOCKED_BY_LIVE_CONNECTOR |

## Batch 4：Workflow binding 与 event bus

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-400 | Temporal | typed snapshot/ref Activity adapter | deterministic history、retry、hash assertion；[Temporal binding](phase-6-temporal-binding.json) 缺 native history/replay evidence | BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE |
| P6-401 | Conductor | task input/output adapter | receipt polling、timeout、replay；[Conductor binding](phase-6-conductor-binding.json) 缺 native task/replay evidence | BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE |
| P6-402 | NATS | registry-backed event/replay API | subject ACL、durable、DLQ、backpressure；[NATS event](phase-6-nats-event.json) 缺 native outage/replay evidence | BLOCKED_BY_LIVE_EVENT_EVIDENCE |
| P6-403 | Record Hub | event-to-table state mapping | source pointer、gap/conflict、rebuild；[event table](phase-6-event-table.json) 缺 native replay/rebuild evidence | BLOCKED_BY_LIVE_EVENT_EVIDENCE |

## Batch 5：自助化 control plane 与运营

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-500 | Web/API | table/schema/tag/relation console | [control-plane contract](phase-6-control-plane.json)；workspace-scoped tag dictionary Mongo/CAS API、RBAC、assignment 校验和 relation/tag console 已接入，仍缺 role/negative matrix 与 redacted export receipt | BLOCKED_BY_CONTROL_PLANE_GAP |
| P6-501 | Web/API | connector onboarding workflow | [onboarding contract](phase-6-connector-onboarding.json)；registry/manifest 已存在，自助 onboarding API/console 缺失 | BLOCKED_BY_ONBOARDING_API_GAP |
| P6-502 | Ops | observation/reconciliation dashboard | [observation contract](phase-6-observation-dashboard.json)；operations/retry/rebuild 基础存在，finding/reconciliation signal 缺失 | BLOCKED_BY_OBSERVABILITY_GAP |
| P6-503 | Ops | operator runbook and rollback | [operator runbook contract](phase-6-operator-runbook.json)；静态 procedure 已固化，缺 native rollback drill evidence | BLOCKED_BY_LIVE_ROLLBACK_EVIDENCE |

## Batch 6：规模与多地域评估

| ID | 范围 | 任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| P6-600 | 四仓库 | production-like mixed load | [mixed-load contract](phase-6-mixed-load.json)；synthetic capacity 和 bounded budget 基础存在，缺四 owner live evidence | BLOCKED_BY_LIVE_CAPACITY_EVIDENCE |
| P6-601 | 数据层 | archive/retention/restore extension | [retention/restore contract](phase-6-retention-restore.json)；已有静态 PITR/archive/rebuild 边界，缺隔离 live restore evidence | BLOCKED_BY_LIVE_RESTORE_EVIDENCE |
| P6-602 | 架构 | multi-region decision record | [multi-region decision](phase-6-multi-region.json)；默认 single-region，缺 capacity/RPO/RTO/cost/signoff live evidence | BLOCKED_BY_LIVE_MULTI_REGION_EVIDENCE |

## 执行约束

- P6-000 未解除前只做文档、contract 和 fixture，不启动真实新 connector 流量。
- 每项跨仓库任务必须在各仓库独立提交，并把实际 commit/artifact/evidence 写入 manifest。
- 任何 `SKIPPED`、`PARTIAL`、`UNVERIFIED` 或敏感字段泄漏都阻止下一批 live work。
