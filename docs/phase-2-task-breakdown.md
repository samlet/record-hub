# Integration Beta 任务分解

- 状态：In progress
- 方案：[phase-2-evaluation.md](phase-2-evaluation.md)
- 需求：[phase-2-requirements.md](phase-2-requirements.md)
- 开始日期：2026-09-17

状态沿用 MVP 定义：`TODO`、`IN_PROGRESS`、`PARTIAL`、`DONE`、`BLOCKED`、`SKIPPED`、
`DEFERRED`。Live 依赖不可用时必须记录 `SKIPPED` 与重试条件，不能以 fake 替代。

## P2-0：身份与隔离验收基线

| ID | 任务 | 状态 | 验收 |
| --- | --- | --- | --- |
| P2-0-001 | Human/workload identity ADR | DONE | ADR-0003 冻结 issuer、claim、scope、policy、本地/Beta/生产边界 |
| P2-0-002 | Exact machine policy runtime config | DONE | 有界严格 JSON；issuer/subject/audience/scope/tenant/workspace/purpose/resource exact match；负向测试通过 |
| P2-0-003 | Ephemeral local workload issuer | DONE | client credentials、临时 RS256、5 分钟 token、discovery/JWKS；仅本地/CI |
| P2-0-004 | Workload token live contract gate | DONE | Fluxion/Bids 独立 client；错误 secret/scope 拒绝；`make p2-workload-identity-smoke` 通过 |
| P2-0-005 | 隔离 Mongo/NATS/Dex/Workload issuer/API 拓扑 | DONE | `make p2-isolated-core` 使用专用端口/临时目录/数据库，一条命令启动、探测、验收、清理；Mongo/NATS/Dex/Record Hub 与 workload token 均通过 |
| P2-0-006 | 隔离 Temporal/Conductor/四应用拓扑 | PARTIAL | P2-0-006a 引擎基线已通过；四应用受监督接入仍需单独完成，不能复用共享运行时 |
| P2-0-006a | 隔离 Temporal/Conductor 引擎基线 | DONE | `make p2-engine-foundation` 使用专用端口和临时持久化，真实 Temporal/Conductor 启动、健康探测、退出清理通过 |
| P2-0-006b | Approver/Fluxion/Bids/Record Hub 业务接入 | TODO | 四进程分别指向隔离身份、Mongo/NATS、Temporal/Conductor；保存版本、配置摘要和有界日志 |
| P2-0-007 | 补跑 Dex 浏览器角色矩阵 | SKIPPED | Dex 协议与 PKCE 已通过，但本批未运行真实浏览器 login/callback/logout、OWNER/EDITOR/VIEWER、撤权和跨租户矩阵；待 Playwright/Chrome 与 membership provisioning 拓扑就绪后重试 |
| P2-0-008 | 补跑双引擎 Binding live/restart | SKIPPED | Temporal/Conductor 引擎基线与 workload token contract 已通过，四业务进程接入（P2-0-006b）尚未完成，因此未宣称真实 snapshot/replay/restart；完成 006b 后重试 |
| P2-0-009 | 补跑四系统 outage/restart/rotation | SKIPPED | 当前没有可独立启动的 Approver/Fluxion/Bids/Record Hub 监督式拓扑，未运行 producer backlog/recovery、全进程 restart、JWKS/client-secret rotation；完成 006b 与 rotation 配置后重试 |
| P2-0-010 | P2-0 基线验收报告 | DONE | [phase-2-acceptance-report.md](phase-2-acceptance-report.md) 固化命令、版本、commit、PASS/SKIPPED、重试条件与遗留风险 |

## 后续批次

| 批次 | 状态 | 范围 |
| --- | --- | --- |
| P2-1 Operable Data | IN_PROGRESS | Schema diff/migration、source/mapping registry、projection rebuild、SLO |
| P2-2 Realtime Read Feed | IN_PROGRESS | SSE、cursor、背压、撤权和配额 |
| P2-3 Controlled Command | IN_PROGRESS | policy、receipt、owner Inbox、单一低风险 command |
| P2-4 Approval/Beta | TODO | Approver connector/试点、备份恢复、升级回滚、Beta 准入 |

P2-0-005 的运行说明见 [deploy/local/isolated/README.md](../deploy/local/isolated/README.md)。

## P2-1 当前任务

| ID | 任务 | 状态 | 验收 |
| --- | --- | --- | --- |
| P2-1-001 | Schema compatibility diff API | DONE | `POST /api/v1/schemas/{schemaId}/compatibility` 以 OWNER 权限比较已发布版本和候选 JSON Schema，返回排序稳定的 JSON Pointer/path、kind、message；正向、breaking、非法对象和 Editor 拒绝测试通过 |
| P2-1-002 | Explicit migration plan | PARTIAL | [phase-2-migration-design.md](phase-2-migration-design.md) 的计划登记/读取/取消 API、Mongo unique/index、compatibility hash、幂等和状态测试已完成；执行/恢复及 breaking publish 强制关联仍待实现 |
| P2-1-003 | Source/mapping registry | DONE | 控制面、fixture 发布门、不可变 published generation、tenant/workspace/source/type/version 精确索引、原子 active pointer、last-known-good refresh、动态 projector 与 Mongo/JetStream durable restart recovery 均通过；`make p2-mapping-recovery` |
| P2-1-004 | Projection rebuild operation | DONE | Owner API、operation receipt、CAS 状态机、Mongo raw event archive、隔离 staging records、版本单调 replay、checkpoint 补齐、read pointer 原子切换、取消/失败恢复和 live continuation 已完成；归档启用前的历史事件不在可重放窗口内，超过 100,000 条的 scope 需先扩展 archive 分页策略 |
| P2-1-005 | Query cost and SLO baseline | DONE | [phase-2-slo-design.md](phase-2-slo-design.md) 固化 page/response/time budget、查询拒绝、freshness snapshot、lag/backlog/error budget 指标、可配置阈值和 `slo_breach` 告警谓词；外部通知由部署层消费 metrics |

## P2-2 当前任务

| ID | 任务 | 状态 | 验收 |
| --- | --- | --- | --- |
| P2-2-001 | Bounded record feed broker | DONE | 按 tenant/workspace/table scope fan-out；2,048 条有界历史、单调 SSE id、游标过期和慢消费者断开单测通过 |
| P2-2-002 | SSE HTTP contract | DONE | `GET /api/v1/tables/{tableId}/records/stream` 支持 `Last-Event-ID`/`cursor`、引用事件、heartbeat、64 KiB 事件上限、256 连接上限；OpenAPI 已更新 |
| P2-2-003 | Mutation/projection publication | DONE | 普通记录 create/update/delete 与 live projection apply 发布引用事件；projection retry 以 source event dedup；staging replay 不发布 |
| P2-2-003a | Backpressure and connection quotas | PARTIAL | 单连接缓冲、单事件大小、principal/scope 连接数和进程连接数有界；速率令牌桶和跨实例 quota 尚未实现，待 Beta capacity baseline |
| P2-2-004 | Revocation and live acceptance | PARTIAL | 每 5 秒重新授权并撤权后关闭连接；单测和错误路径已通过，真实 Dex 浏览器撤权、跨实例恢复和容量基线待 P2-0-007/009 及 Beta 拓扑就绪后补跑 |

## P2-3 当前任务

| ID | 任务 | 状态 | 验收 |
| --- | --- | --- | --- |
| P2-3-001 | Exact command policy registry | DONE | `RECORD_HUB_COMMAND_POLICIES` 严格 JSON、无通配符；workload identity 的 issuer/audience/scope/resource 精确匹配 |
| P2-3-002 | Operation receipt and idempotency | DONE | Mongo unique receipt、payload canonical hash、相同键 replay、hash 冲突拒绝、状态枚举和审计摘要已实现 |
| P2-3-003 | Controlled command HTTP contract | DONE | `POST/GET /api/v1/commands`、expectedVersion、256 KiB payload limit、OpenAPI 和负向测试已完成 |
| P2-3-004 | Owner Inbox and result event | PARTIAL | `COMMAND_RESULTS` stream、Record Hub durable result pull/CAS/幂等终态推进、Mongo/Memory Inbox claim/commit 抽象和安全失败结果已完成；真实 Approver/Fluxion/Bids 业务库事务、outbox、ACK-loss/restart live 证据待四系统接入 |
| P2-3-004a | Result event consumer | DONE | `results.<ownerSystem>.<action>.v1`、256 KiB bounded decode、terminal status CAS、event ID replay/conflict、DLQ runner、审计摘要和负向单测 |
| P2-3-004b | Owner Inbox contract | PARTIAL | Mongo unique Inbox + revision CAS、Memory 测试实现和 callback/outbox 边界已完成；各 owner 仓库仍需接入自己的事务与 outbox |

真实 Approver、Fluxion、Bids 接入已转入 [Phase 3 任务分解](phase-3-task-breakdown.md)；本表保留平台侧
基线状态，不提前把 owner transaction、审批试点或 live 故障矩阵标为完成。
