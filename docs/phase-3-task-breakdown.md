# Phase 3 真实业务系统接入任务分解

- 日期：2026-09-18
- 状态：Batch 4H（upgrade/rollback gate）完成；P3-308、P3-409 仍待后续批次
- 方案：[phase-3-integration-design.md](phase-3-integration-design.md)
- 需求：[phase-3-requirements.md](phase-3-requirements.md)
- 验收：[phase-3-acceptance-plan.md](phase-3-acceptance-plan.md)

状态：`TODO`、`IN_PROGRESS`、`PARTIAL`、`DONE`、`BLOCKED`、`SKIPPED`、`DEFERRED`。
每批必须分别在涉及的仓库提交；跨仓库 gate 记录所有 commit SHA，不以未提交工作树作为验收基线。

## 当前能力盘点

| ID | 能力 | 状态 | 证据/缺口 |
| --- | --- | --- | --- |
| P3-BASE-001 | Record Hub command gateway/result consumer | DONE | receipt、policy、JetStream publish、result CAS 和 durable consumer 已实现 |
| P3-BASE-002 | 三系统摘要 Outbox | DONE | Approver Application、Fluxion Project、Bids Tender 均有本地事务 Outbox 与 NATS relay |
| P3-BASE-003 | Workflow Binding | PARTIAL | Fluxion Temporal 与 Bids Conductor adapter 已实现；真实全拓扑和 Approver 使用场景未验收 |
| P3-BASE-004 | Owner command processing | PARTIAL | 三个业务系统已接入 strict command durable、原生 Inbox 和 result Outbox；真实四进程故障矩阵留 P3-308 |
| P3-BASE-005 | Approver service integration | PARTIAL | Approver generic connector 基座已存在；Fluxion/Bids connector 未实现 |
| P3-BASE-006 | 隔离四应用拓扑 | DONE | P3-400 原生 supervisor 已能在专用端口/临时 DB 启动四个真实应用及基础设施并通过 readiness；业务操作矩阵仍留 P3-308/P3-402 |

## Batch 0：方案、契约与仓库基线

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-000 | Record Hub | 冻结 Phase 3 方案、需求、任务和验收计划 | DONE | 四份文档交叉链接，明确 owner/非目标/Gate |
| P3-001 | 四仓库 | 记录语言、数据库、engine、migration 和现有 integration inventory | DONE | [phase-3-baseline-manifest.json](phase-3-baseline-manifest.json) 固定四仓库 commit、构建命令和依赖版本 |
| P3-002 | Record Hub | 建 command/result v1 schema、safe fixtures、error codes、manifest | DONE | `contracts/commands` strict JSON、canonical payload hash、valid/invalid fixture 和 error catalog 已测试 |
| P3-003 | Approver/Fluxion/Bids | 镜像 contract assets | DONE | 四仓库 8 个 asset 逐字节一致，见 `make p3-contract-gate` |
| P3-004 | 四仓库 | 建 contract mirror CI gate | DONE | `scripts/verify-p3-contract-mirrors.sh` 检查缺失、漂移、manifest hash 和 JSON 资产 |
| P3-005 | Record Hub | 发布 owner adapter 接入说明和示例，不发布内部 Mongo 实现包 | DONE | v1 schema、fixture、错误分类和 Envelope wire 语义可供 Java/Kotlin/Go 独立实现 |
| P3-006 | 四仓库 | 固定每仓库基线 commit、构建命令和依赖版本 | DONE | 机器可读 baseline manifest 已提交；后续批次以该基线记录 commit 演进 |

## Batch 1：Fluxion Controlled Command

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-100 | Fluxion | 增加 command Inbox 与 result Outbox Flyway migration | DONE | V8 migration 建表、状态约束、operation/event unique、scope/dispatch index；当前切片基线 commit `3530d96` |
| P3-101 | Fluxion | 实现 command envelope strict decoder/validator | DONE | 256 KiB、UTF-8、未知字段、尾随 JSON、payload canonical hash、owner/action subject 校验已测试 |
| P3-102 | Fluxion | 实现专属 JetStream durable pull runner | DONE | `fluxion-command-inbox-v1`、`OWNER_COMMANDS`、explicit ACK、NAK backoff、terminal poison、重连循环 |
| P3-103 | Fluxion | 实现 Inbox claim/replay/hash-conflict | DONE | Exposed transaction claim、相同 operation/hash replay、scope/action/hash conflict；与领域及 result Outbox 同事务处理 |
| P3-104 | Fluxion | 实现 `project.annotate` APPEND/VOID | DONE | integration event append/void、annotation allowlist、active target/version/tenant/workspace 校验；不改变 workflow stage |
| P3-105 | Fluxion | 领域事务写 summary version/event Outbox/result Outbox | DONE | Exposed outer transaction 包含 Inbox claim、project event、summary version/Outbox、result Outbox；commit-before-ACK |
| P3-106 | Fluxion | 实现 result Outbox relay | DONE | `results.fluxion.<action>.v1`、event ID message ID、lease/retry/dead、ACK-loss 单测 |
| P3-107 | Fluxion | 增加 Inbox/Outbox 运维查询和安全 metrics | DONE | supervisor-only `/api/operations/record-hub/commands` 仅返回状态计数/backlog，不返回 ID、payload 或 secret |
| P3-108 | Record Hub | 增加真实 Fluxion policy fixture/config | DONE | `deploy/local/p3/fluxion-project-annotate-policy.json` 固定 workload issuer/subject/audience/scope、tenant/workspace/purpose/action |
| P3-109 | Record Hub/Fluxion | command/result 跨仓库 contract test | DONE | Kotlin/Go runtime、四仓库 hash gate 和 P3-110 live APPEND/VOID、replay、hash-conflict、version/error-redaction matrix 均通过 |
| P3-110 | Record Hub/Fluxion | live command E2E | PARTIAL | 核心切片与 `make p3-fluxion-command-faults` 的双 worker、commit-before-ACK replay、result consumer restart、NATS outage/backlog recovery、result poison DLQ、跨系统同时重启、双 result worker 竞争已实现；更细粒度 transaction rollback/发布前崩溃仍待后续批次 |
| P3-111 | Record Hub/Fluxion | annotation semantic compensation | PARTIAL | payload allowlist、VOID target/重复/版本/workspace/tenant 负向单测已完成；真实 DB 审计与跨项目 Gate 待 live 验收 |

## Batch 2：Fluxion → Approver 审批试点

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-200 | 四仓库 | 冻结 dispatch approval request/result v1 contract | DONE | 四仓库 schema/fixture/error/manifest byte-identical；`make p3-contract-gate`；Approver `ef010af`、Fluxion `91df9e9`、Bids `60fd395`、Record Hub `4d7658c` |
| P3-201 | Fluxion | 增 external approval request/outbox/result Inbox migration | DONE | Fluxion `104edac`；V9 在临时 PostgreSQL 从 V1 全量应用；single-active generation、decision version unique、tenant/workspace index；V10 update outbox 随后加入 |
| P3-202 | Fluxion | 低置信度 Activity 写稳定 request + Outbox | PARTIAL | `104edac` Activity stable `apr-...` ID、proposal/snapshot hash、retry 幂等单测通过；Temporal live history/真实 Approver 回路留 P3-212/213 |
| P3-203 | Fluxion | Approver HTTP client/sender | DONE | Fluxion `1e1db27`；独立 headers、Idempotency-Key、timeout、2xx/4xx/5xx typed error、lease/retry relay 测试通过 |
| P3-204 | Approver | 增 `FLUXION` request contract handler | DONE | Approver `848f785`；registry 精确匹配、严格 unknown-field/hash/generation/resource 校验，未映射请求 fail closed |
| P3-205 | Approver | 配置 tenant/org/process/requester mapping | DONE | Approver `d5f1e64`；`scripts/bootstrap-fluxion-approval-mapping.sql` 幂等配置 connection/mapping/binding，wire body 不覆盖 server mapping，requester 未映射直接失败 |
| P3-206 | Approver | materialize immutable Application/process fixture | DONE | Approver `888ecb2`；bounded snapshot projector 增加 workflow/generation/proposal 字段，通用 materializer 继续使用稳定 business key；真实 published process fixture 留 live gate |
| P3-207 | Approver | 增 Fluxion connector Action | DONE | Approver `be7f74e`；`fluxion.dispatch.apply@v1` 严格重验 proposal hash/generation/resource，stable action key 与错误分类单测通过 |
| P3-208 | Approver | 增 result contract/delivery/reconciliation handler | DONE | Approver `888ecb2`；Fluxion result contract + generic result dispatcher handler，复用现有 outbox/reconciliation，不新增扫描器 |
| P3-208a | Approver | 扩展 `WITHDRAWN/EXPIRED` 终态 | DONE | Approver `888ecb2`；V31 decision constraint、enum、Fluxion handler 支持扩展终态，旧 handler 测试回归通过 |
| P3-209 | Fluxion | integration-only result endpoint/Inbox | DONE | Fluxion `bbf37b2`；`/api/integrations/fluxion/approval-results` 仅 service token，tenant/workspace/resource/version/workflow/hash 校验，duplicate/late conflict fail closed |
| P3-210 | Fluxion | result → Temporal update Outbox | DONE | Fluxion `5038d06`；Inbox 与 update Outbox 同事务，stable `(externalRequestId, decisionVersion)`，worker relay 异步 update、retry/dead/ACK-loss 单测 |
| P3-211 | Fluxion | feature flag 与本地 human task fallback | DONE | Fluxion `1321285`；默认关闭；开启时低置信度不双写本地任务；旧 history 默认字段兼容，关闭不影响本地 fallback |
| P3-212 | Approver/Fluxion | 五类终态 E2E | SKIPPED | 当前无可复现 published Approver process、Dex workload token 与真实隔离拓扑；见 [phase-3-live-skip-log.md](phase-3-live-skip-log.md) |
| P3-213 | Approver/Fluxion | timeout、晚到、重启、reconciliation E2E | SKIPPED | 依赖 P3-212 live topology；不以 fake server、内存 store 或手工改库替代；见 [phase-3-live-skip-log.md](phase-3-live-skip-log.md) |
| P3-214 | Record Hub | 审批关联安全投影 | DONE | Record Hub `727b1ba`；严格 approval summary v1、exact handler、独立投影 table/schema、Application/Project/workflow ref、status/decision version/freshness；敏感字段拒绝测试通过 |

## Batch 3：Approver 与 Bids Owner Adapter

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-300 | Approver | command Inbox/result Outbox Flyway migration | DONE | Approver `3194bf8`；V32 建 Inbox/Outbox/annotation 表，tenant/version/unique/lease/index 约束已静态验收 |
| P3-301 | Approver | `application.annotate` handler | DONE | Approver `3194bf8`；只追加 integration note，expected version/tenant/application/VOID 校验，不改变审批状态/任务决定 |
| P3-302 | Approver | command durable + result relay | DONE | Approver `3194bf8`；`approver-command-inbox-v1` explicit ACK、NAK/terminal poison、result Outbox lease relay；重启/ACK-loss live matrix 留 P3-308 |
| P3-303 | Bids | command Inbox/result Outbox migration | DONE | Bids `c39e07f`；PostgreSQL migration V27 与 SQLite AutoMigrate 对齐，状态/unique/lease/index 约束已验收 |
| P3-304 | Bids | `tender.annotate` handler | DONE | Bids `c39e07f`；tenant/org/tender/version/annotation 校验；不读写报价/投标/文件/定标/合同/付款字段 |
| P3-305 | Bids | command durable + result relay | DONE | Bids `c39e07f`；`bids-command-inbox-v1` explicit ACK、NAK/terminal poison、result Outbox lease relay；重启/ACK-loss live matrix 留 P3-308 |
| P3-306 | 三 owner | 共享行为 fixture | DONE | Record Hub `0fb5750`；Approver/Bids/Fluxion 均覆盖 duplicate/hash/version、strict payload、note-only APPEND/VOID 和 commit-before-ACK 代码路径 |
| P3-307 | Record Hub | 三条 exact command policy 与 receipt 运维视图 | DONE | Record Hub `0fb5750`；三份默认关闭的 exact policy fixture，`/api/v1/operations/commands` 仅返回 scope/status/backlog 汇总，不返回 payload、ID 或 secret |
| P3-308 | 四仓库 | 三 owner live matrix | SKIPPED | 当前缺少可复现的四应用隔离拓扑、Dex workload token、Approver/Fluxion/Bids 同时运行环境；见 [phase-3-live-skip-log.md](phase-3-live-skip-log.md) |

## Batch 4：隔离全拓扑与 Beta 准入

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-400 | Record Hub | 扩展原生监督脚本启动四应用 | DONE | `scripts/verify-p3-four-owner-topology.sh`；原生 Mongo/NATS/Dex/workload issuer/Temporal/Conductor + Record Hub/Approver/Fluxion/Bids API/worker 隔离启动；独立端口/DB/PID/log，退出只清理自身进程；2026-09-19 Darwin arm64 live PASS，evidence manifest 记录四仓库 SHA |
| P3-401 | 四仓库 | 自动准备 schema、mapping、policy、tenant/org fixture | DONE | `scripts/bootstrap-p3-fixtures.sh`；topology/owner/policy/schema/binding-policy 生成到临时目录，重复运行幂等，6 个文件 manifest SHA 固定；同一 live evidence 的 fixture manifest 已验收 |
| P3-402 | 四仓库 | 真实 Temporal + Conductor workflow E2E | DONE | `make p3-workflow-e2e` 在隔离四 owner 拓扑中通过 Fluxion command→Temporal snapshot、Bids approval→Conductor snapshot、Mongo binding_snapshots、Inbox/Result Outbox、history safe-marker 断言；见 [phase-3-batch4b-workflow-e2e.md](phase-3-batch4b-workflow-e2e.md) |
| P3-403 | 四仓库 | NATS outage/backlog/recovery | DONE | `make p3-nats-outage-recovery`；真实 Fluxion Temporal project summary 在 NATS 停机时进入 owner outbox，恢复同一 JetStream store 后 outbox 全部 SENT、Mongo Inbox/APPLIED 与最高 source version 对齐；evidence `nats-outage-recovery.json` |
| P3-404 | 四仓库 | 进程 restart/ACK-loss 矩阵 | DONE | `make p3-process-restart-ack-loss`；真实 P3-110 fault matrix 验证 owner commit-before-ACK、result publish-before-SENT、consumer pause/restart、cross restart、durable consumer competition 和 safe DLQ follow-up；证据 `p3-404-restart-ack-loss.json` |
| P3-405 | 四仓库 | workload/JWKS/secret rotation | SKIPPED | `make p3-credential-rotation` 已通过 OIDC JWKS rotation 与 workload secret fail-closed contract checks；四 owner overlap SKIPPED：本地 issuer 启动时生成单 key，暂无热轮换/JWKS overlap 和 owner credential reload 协议；证据 `credential-rotation.json` |
| P3-406 | 四仓库 | Mongo/PostgreSQL/JetStream 备份恢复 | SKIPPED | `make p3-backup-restore` 已交付原生 `mongodump/mongorestore`、`pg_dump/pg_restore`、JetStream store archive 与 hash manifest；当前四 owner supervisor 会在 teardown 删除临时 source/restore 目标，未提供持久隔离恢复目标，故 live restore SKIPPED；证据 `backup-restore.json` |
| P3-407 | 四仓库 | 容量与 recovery baseline | PARTIAL | `make p3-capacity-baseline` 对真实 Mongo/NATS/Record Hub projection 跑 bounded summary load，输出 publish/drain rate、p50/p95/p99/max latency；当前仅 synthetic transport/projection baseline，四 owner mixed workflow load SKIPPED，证据 `capacity-metrics.json` |
| P3-408 | 四仓库 | 升级/回滚演练 | SKIPPED | `make p3-upgrade-rollback` contract checks PASS；真实 rolling upgrade/rollback 因缺少 previous-version/new-version worker artifacts、隔离目标和 rollback runner SKIPPED；证据 `upgrade-rollback.json` |
| P3-409 | Record Hub | Phase 3 验收报告 | TODO | commit、版本、PASS/SKIPPED/FAIL、风险 owner、重试条件完整 |

## 依赖与顺序

```text
P3-002..006
    ↓
P3-100..111 Fluxion command gate
    ↓
P3-200..214 Approver pilot gate
    ↓
P3-300..308 三 owner gate
    ↓
P3-400..409 Beta readiness gate
```

- P3-110 未通过前不得增加第二个 Fluxion command。
- P3-212/213 未通过前不得把 Bids 高敏节点接入 Approver。
- P3-408 未通过前不得删除旧 worker、旧 schema reader 或 Fluxion 本地审批 fallback。
- live 依赖缺失时只可标记 `SKIPPED`，不能用 fake server、内存仓库或手工改库代替。
