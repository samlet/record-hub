# Phase 4 Integration Beta 任务分解

- 日期：2026-09-20
- 状态：Batch 0 已完成；Batch 1/2/3/4 静态收口完成，Batch 1/2/3/4 live 验收待隔离环境
- 方案：[phase-4-design.md](phase-4-design.md)
- 需求：[phase-4-requirements.md](phase-4-requirements.md)
- 验收：[phase-4-acceptance-plan.md](phase-4-acceptance-plan.md)

本任务表属于 Record Hub 跨仓库集成计划。每批必须在涉及仓库独立提交，跨仓库 gate 的 evidence
manifest 必须记录当次实际 commit 和 artifact checksum。

## Batch 0：契约与证据基线

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-000 | Record Hub | Phase 4 方案、需求、任务与验收计划 | P3 report | DONE | 四份文档互链，阶段边界、Gate 和禁止项一致 |
| P4-001 | 四仓库 | 固定当前 commit、artifact、migration、contract baseline | P4-000 | DONE | `make p4-baseline` 通过；四仓库 commit/source digest 与 6 个构建 artifact checksum 已写入 [baseline manifest](phase-4-baseline-manifest.json) |
| P4-002 | 四仓库 | contract/fixture inventory 与 mirror gate | P4-001 | DONE | `make p4-contract-inventory` 通过；command/result、dispatch approval、summary owner manifests 与 schema hash 无漂移，详见 [contract inventory](phase-4-contract-inventory.json) |
| P4-003 | Record Hub | Phase 4 evidence manifest/schema 与持久目录 | P4-001 | DONE | `make p4-evidence-contract` 通过；schema、valid/invalid fixture、redacted path 约束与 `build/evidence/phase4/<run-id>` 布局已固定 |
| P4-004 | 四仓库 | 扩展隔离 topology 与 deterministic fixtures | P4-001..003 | DONE | `make p4-fixtures` 通过；tenant/org、approval slice、recovery target、expand-contract 与 OFF-by-default flags 可重复生成 |

## Batch 1：Phase 3 收口

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-100 | Record Hub | reloadable workload issuer 与双 JWKS key overlap | P4-004 | PARTIAL | `tools/workload-issuer` 已支持 SIGHUP、clients file reload、双 JWKS overlap 和过期移除；单测 PASS，四 owner live overlap SKIPPED |
| P4-101 | 四仓库 | workload client secret reload/rotation | P4-100 | PARTIAL | 六个独立调用方向已进入 Batch 1 spec；owner in-place reload、双 secret overlap 和无副作用 live 验收 SKIPPED |
| P4-102 | 四仓库 | Mongo/PostgreSQL/JetStream recovery set | P4-004 | PARTIAL | 原生 dump/restore/archive runner 与 count/hash/index/version/cursor 断言入口已复用；未提供显式 source/restore targets，live SKIPPED |
| P4-103 | 四仓库 | mixed owner capacity/recovery harness | P4-004 | PARTIAL | 10k history、50/200 msg/s、100 pending、10 分钟 drain 规格已固定；四 owner mixed live load SKIPPED |
| P4-104 | 四仓库 | old/new rolling upgrade/rollback harness | P4-002,004 | PARTIAL | expand/contract、durable、feature flag、old worker 规则与兼容性 gate 已固定；immutable old/new artifacts 与隔离目标缺失，live SKIPPED |
| P4-105 | Record Hub/Fluxion | transaction rollback 与 publish-before-SENT crash | P4-004 | SKIPPED | 现有 P3 runner 尚未暴露两处精确 fault injection boundary；保留为下一批 live 前置，不以近似 ACK-loss 替代 |
| P4-106 | 四仓库 | 三 owner 同时运行 live behavior matrix | P4-004 | PARTIAL | 三 owner contract matrix 入口 PASS；四应用同时运行及 duplicate/hash/version/ACK loss/restart/DLQ live matrix SKIPPED |

## Batch 2：Fluxion Approval Beta

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-200 | Fluxion | tenant/workspace feature flag 与在途 drain | P4-101,106 | PARTIAL | 静态 config/relay/result boundary 与 Fluxion 全量单测 PASS；C-001/C-008 live 因隔离拓扑缺失 SKIPPED，详见 [P4-200 closure](phase-4-batch2-p4-200.md) |
| P4-201 | Fluxion/Approver | 五类终态与稳定 request state machine | P4-200 | PARTIAL | 五类终态、alias、同事务 request/InBox/update-outbox 状态机与单测 PASS；C-003..006 live 因隔离拓扑缺失 SKIPPED，详见 [P4-201 closure](phase-4-batch2-p4-201.md) |
| P4-202 | Fluxion/Approver | late result、generation guard 与 reconciliation | P4-201 | PARTIAL | late result 不更新 Temporal；VERSION_DRIFT/LATE_RESULT finding、operator view 与审计静态/单测 PASS，跨服务 live SKIPPED，详见 [P4-202 closure](phase-4-batch2-p4-202.md) |
| P4-203 | Fluxion | local human fallback 与 replay/Continue-As-New | P4-200..202 | PARTIAL | local fallback、stable request ID、replay/CAN 不重复 Application/update 单测 PASS；Temporal+Approver live SKIPPED，详见 [P4-203 closure](phase-4-batch2-p4-203.md) |
| P4-204 | Record Hub | Project↔Application typed association projection/API | P4-002,201 | PARTIAL | Mongo projection/API、monotonic version、gap/conflict、tenant auth、安全字段和 OpenAPI 单测 PASS；Mongo/NATS live SKIPPED，详见 [P4-204 closure](phase-4-batch2-p4-204.md) |
| P4-205 | 四仓库 | Fluxion Approval Beta fault matrix | P4-200..204 | PARTIAL | 五类 response loss/restart/NATS outage/late result/flag rollback 静态 contract PASS；隔离拓扑 live 全部 SKIPPED，详见 [P4-205 closure](phase-4-batch2-p4-205.md) |

## Batch 3：Bids Approval Pilot

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-300 | Approver/Bids | 招标准备发布 approval contract 与 allowlist fixture | P4-002 | PARTIAL | [closure](phase-4-batch3-p4-300.md)；静态 PASS，live SKIPPED |
| P4-301 | Bids | request/result Inbox/Outbox migration 与 repository | P4-300 | PARTIAL | [closure](phase-4-batch3-p4-301.md)；静态 PASS，live SKIPPED |
| P4-302 | Bids/Approver | request relay、materializer 与 result dispatcher | P4-301 | PARTIAL | [closure](phase-4-batch3-p4-302.md)；静态 PASS，live SKIPPED |
| P4-303 | Bids | result apply、单一审批 authority 与 Conductor task-completion Outbox | P4-302 | PARTIAL | [closure](phase-4-batch3-p4-303.md)；静态 PASS，live SKIPPED |
| P4-304 | Record Hub | Tender↔Application association projection/API | P4-204,302 | PARTIAL | [closure](phase-4-batch3-p4-304.md)；静态 PASS，live SKIPPED |
| P4-305 | 四仓库 | Bids approval live/fault/security matrix | P4-300..304 | PARTIAL | [closure](phase-4-batch3-p4-305.md)；静态 PASS，live SKIPPED |

## Batch 4：运维与安全

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-400 | 四仓库 | tenant/organization/identity/ACL negative matrix | P4-205,305 | PARTIAL | [closure](phase-4-batch4-p4-400.md)；四 owner 静态拒绝边界 PASS，隔离身份 live SKIPPED |
| P4-401 | 四仓库 | 指标、SLO snapshot 与有限基数检查 | P4-103,205,305 | PARTIAL | [closure](phase-4-batch4-p4-401.md)；bounded metrics/static SLO PASS，混合压测 live SKIPPED |
| P4-402 | Record Hub | approval association/operator/reconciliation UI | P4-204,304,401 | PARTIAL | [closure](phase-4-batch4-p4-402.md)；只读控制台与 403/405 回归 PASS，角色 live SKIPPED |
| P4-403 | 四仓库 | secret/PII/Bids sealed-data evidence scan | P4-400 | PARTIAL | [closure](phase-4-batch4-p4-403.md)；源码/evidence 静态扫描 PASS，真实日志/DLQ live SKIPPED |
| P4-404 | 四仓库 | rotation/recovery/reconciliation runbook | P4-100..104,401 | PARTIAL | [closure](phase-4-batch4-p4-404.md)；runbook 与入口 PASS，恢复演练 live SKIPPED |

## Batch 5：Beta 发布

| ID | 仓库 | 任务 | 依赖 | 状态 | 验收 |
| --- | --- | --- | --- | --- | --- |
| P4-500 | 四仓库 | release candidate manifest 与 immutable artifacts | Batch 0..4 | TODO | commit/checksum/migration/contract/config 摘要固定 |
| P4-501 | 四仓库 | 完整 rotation + restore + mixed load + rolling rollback | P4-500 | TODO | P4-G1..G5 必选 live cases 全 PASS，无 SKIPPED/PARTIAL |
| P4-502 | 四仓库 | 单 tenant Beta 灰度与观察窗口 | P4-501 | TODO | 至少一个完整 retention/retry window；SLO/告警/finding 可接受 |
| P4-503 | Record Hub | Phase 4 Beta 验收报告 | P4-502 | TODO | commit/artifact、case、RPO/RTO、容量、风险 owner、rollback、签字完整 |
| P4-504 | 四仓库 | fallback/旧版本去留评审 | P4-503 | TODO | 单独决策；未批准则继续保留，不作为报告完成的隐含动作 |

## 依赖与执行规则

```text
Batch 0 contract/evidence
        ↓
Batch 1 Phase 3 closure
        ↓
Batch 2 Fluxion Beta ──┐
                       ├──> Batch 4 operations/security ──> Batch 5 release
Batch 3 Bids pilot ────┘
```

- Batch 1 是真实 Beta 流量的硬前置；可以并行开发 Batch 2/3，但不能跳过 Gate 发布。
- Batch 2 与 Batch 3 可在契约冻结后并行，必须共享 identity、evidence、finding taxonomy 和安全规则。
- 每批完成都在涉及仓库提交；不得用 Record Hub 单仓库提交代表四仓库已验收。
- live 项缺依赖时标记 `SKIPPED` 并继续可安全完成的任务，但 P4-501 不接受必选项 `SKIPPED`。
- 任何跨租户、敏感泄漏、重复领域副作用、不可恢复 migration 立即阻止后续发布 Gate。
