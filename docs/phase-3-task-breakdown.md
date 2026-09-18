# Phase 3 真实业务系统接入任务分解

- 日期：2026-09-18
- 状态：Batch 0 contract gate complete; owner adapters planned
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
| P3-BASE-004 | Owner command processing | TODO | 三个业务系统均未接入 command durable、原生 Inbox 和 result Outbox |
| P3-BASE-005 | Approver service integration | PARTIAL | Approver generic connector 基座已存在；Fluxion/Bids connector 未实现 |
| P3-BASE-006 | 隔离四应用拓扑 | TODO | P2 只有 core/engine 基线，尚未监督启动四个真实应用进程 |

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
| P3-100 | Fluxion | 增加 command Inbox 与 result Outbox Flyway migration | DONE | V8 migration 建表、状态约束、operation/event unique、scope/dispatch index；commit `ccac8cf` |
| P3-101 | Fluxion | 实现 command envelope strict decoder/validator | DONE | 256 KiB、UTF-8、未知字段、尾随 JSON、payload canonical hash、owner/action subject 校验已测试 |
| P3-102 | Fluxion | 实现专属 JetStream durable pull runner | DONE | `fluxion-command-inbox-v1`、`OWNER_COMMANDS`、explicit ACK、NAK backoff、terminal poison、重连循环 |
| P3-103 | Fluxion | 实现 Inbox claim/replay/hash-conflict | DONE | Exposed transaction claim、相同 operation/hash replay、scope/action/hash conflict；与领域及 result Outbox 同事务处理 |
| P3-104 | Fluxion | 实现 `project.annotate` APPEND/VOID | DONE | integration event append/void、annotation allowlist、active target/version/workspace 校验；不改变 workflow stage |
| P3-105 | Fluxion | 领域事务写 summary version/event Outbox/result Outbox | DONE | Exposed outer transaction 包含 Inbox claim、project event、summary version/Outbox、result Outbox；commit-before-ACK |
| P3-106 | Fluxion | 实现 result Outbox relay | DONE | `results.fluxion.<action>.v1`、event ID message ID、lease/retry/dead、ACK-loss 单测 |
| P3-107 | Fluxion | 增加 Inbox/Outbox 运维查询和安全 metrics | TODO | 无原始 payload/secret，operator scope 与 tenant filter 完整 |
| P3-108 | Record Hub | 增加真实 Fluxion policy fixture/config | TODO | exact workload principal、tenant/workspace/purpose/action |
| P3-109 | Record Hub/Fluxion | command/result 跨仓库 contract test | TODO | success/replay/hash conflict/version conflict/error redaction |
| P3-110 | Record Hub/Fluxion | live command E2E | TODO | commit-before-ACK、ACK loss、双进程竞争、两侧重启无重复副作用 |
| P3-111 | Record Hub/Fluxion | annotation semantic compensation | TODO | VOID 引用校验、重复 VOID、跨项目拒绝和审计通过 |

## Batch 2：Fluxion → Approver 审批试点

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-200 | 四仓库 | 冻结 dispatch approval request/result v1 contract | TODO | schema/fixture/error/manifest mirror 通过 |
| P3-201 | Fluxion | 增 external approval request/outbox/result Inbox migration | TODO | single-active generation、decision version unique、tenant index |
| P3-202 | Fluxion | 低置信度 Activity 写稳定 request + Outbox | TODO | Temporal history 只有 ID/hash；Activity retry 不创建第二申请 |
| P3-203 | Fluxion | Approver HTTP client/sender | TODO | 独立 workload credential、timeout、typed error、receipt recovery |
| P3-204 | Approver | 增 `FLUXION` request contract handler | TODO | registry 精确匹配，unknown/duplicate fail closed |
| P3-205 | Approver | 配置 tenant/org/process/requester mapping | TODO | wire body 不能覆盖服务端 mapping；跨租户拒绝 |
| P3-206 | Approver | materialize immutable Application/process fixture | TODO | stable application/business key；bounded safe snapshot |
| P3-207 | Approver | 增 Fluxion connector Action | TODO | proposal hash/generation 重验；错误分类与 retry policy 正确 |
| P3-208 | Approver | 增 result contract/delivery/reconciliation handler | TODO | 复用 generic dispatcher，无第二套扫描器 |
| P3-208a | Approver | 扩展 `WITHDRAWN/EXPIRED` 终态 | TODO | enum/schema/旧 connector/reconciliation 向后兼容回归通过 |
| P3-209 | Fluxion | integration-only result endpoint/Inbox | TODO | service principal、decision version/hash、晚到结果保护 |
| P3-210 | Fluxion | result → Temporal update Outbox | TODO | transaction 后异步 update；重复 delivery 不重复推进 |
| P3-211 | Fluxion | feature flag 与本地 human task fallback | TODO | 默认关闭；单 generation 不双写；关闭不遗弃在途申请 |
| P3-212 | Approver/Fluxion | 五类终态 E2E | TODO | approved/rejected/withdrawn-or-cancelled/expired/failed 全通过 |
| P3-213 | Approver/Fluxion | timeout、晚到、重启、reconciliation E2E | TODO | 旧 generation 不作用于新 Project；finding 可见可审计 |
| P3-214 | Record Hub | 审批关联安全投影 | TODO | Application/Project ref、状态、freshness；无客户/候选档案 |

## Batch 3：Approver 与 Bids Owner Adapter

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-300 | Approver | command Inbox/result Outbox Flyway migration | TODO | tenant/version/unique/lease/index 门禁通过 |
| P3-301 | Approver | `application.annotate` handler | TODO | 只追加 integration note，不改变审批状态/任务决定 |
| P3-302 | Approver | command durable + result relay | TODO | approver consumer、restart/ACK-loss、DLQ 通过 |
| P3-303 | Bids | command Inbox/result Outbox migration | TODO | PostgreSQL 与 SQLite dev profile 行为明确；生产 gate 用 PostgreSQL |
| P3-304 | Bids | `tender.annotate` handler | TODO | 组织隔离；不读写报价/投标/文件/定标/合同/付款字段 |
| P3-305 | Bids | command durable + result relay | TODO | bids consumer、restart/ACK-loss、DLQ 通过 |
| P3-306 | 三 owner | 共享行为 fixture | TODO | duplicate/hash/version/transaction/ACK-loss 结果一致 |
| P3-307 | Record Hub | 三条 exact command policy 与 receipt 运维视图 | TODO | 默认关闭；按 tenant/workspace 独立启用/撤销 |
| P3-308 | 四仓库 | 三 owner live matrix | TODO | 任一系统 outage 不阻塞其他 owner consumer |

## Batch 4：隔离全拓扑与 Beta 准入

| ID | 仓库 | 任务 | 状态 | 验收 |
| --- | --- | --- | --- | --- |
| P3-400 | Record Hub | 扩展原生监督脚本启动四应用 | TODO | 独立端口/DB/PID/log，退出只清理自身进程 |
| P3-401 | 四仓库 | 自动准备 schema、mapping、policy、tenant/org fixture | TODO | 重复运行幂等，fixture hash 固定 |
| P3-402 | 四仓库 | 真实 Temporal + Conductor workflow E2E | TODO | Binding/command/approval history 与 DB 最终断言 |
| P3-403 | 四仓库 | NATS outage/backlog/recovery | TODO | Outbox 积压可见；恢复清空；无消息丢失/重复副作用 |
| P3-404 | 四仓库 | 进程 restart/ACK-loss 矩阵 | TODO | 每个 commit/ACK 边界注入崩溃并恢复 |
| P3-405 | 四仓库 | workload/JWKS/secret rotation | TODO | overlap 不中断，旧 credential 过期 fail closed |
| P3-406 | 四仓库 | Mongo/PostgreSQL/JetStream 备份恢复 | TODO | hash/count/index/cursor/receipt/Inbox/Outbox 校验通过 |
| P3-407 | 四仓库 | 容量与 recovery baseline | TODO | 数据规模、事件率、p95/p99、backlog drain time 和硬件记录 |
| P3-408 | 四仓库 | 升级/回滚演练 | TODO | contract/migration/worker/feature flag 兼容窗口通过 |
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
