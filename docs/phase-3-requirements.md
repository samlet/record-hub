# Phase 3 真实业务系统接入需求

- 日期：2026-09-18
- 状态：Draft for implementation
- 技术方案：[phase-3-integration-design.md](phase-3-integration-design.md)

## 1. 目标与成功标准

Phase 3 将 Record Hub 与真实 Approver、Fluxion、Bids 进程连接起来。成功不以“消息已发布”判断，
而以 owner 业务事务、result receipt、Workflow 恢复和故障重放均有 live 证据判断。

完成标准：

- Fluxion `project.annotate` 从 Record Hub API 到 Fluxion PostgreSQL 再回到 receipt 终态闭环通过；
- command 重复、payload hash 冲突、版本冲突、ACK 丢失、NATS/owner/Record Hub 重启均无重复副作用；
- Fluxion 低置信度派单可通过 Approver 完成批准、拒绝、撤回/取消、过期和失败路径；
- Approver、Fluxion、Bids 三个真实进程均使用自己的 durable consumer、数据库 Inbox 和 result Outbox；
- Temporal/Conductor history 不保存 command/approval 的原始敏感 payload；
- 一条命令启动隔离四系统拓扑，验收结束后只清理自己启动的进程和临时数据；
- 所有未能 live 验收的条目明确标记 `SKIPPED`，记录原因和重试条件。

## 2. 范围

### 2.1 必须交付

- command/result v1 JSON Schema、fixtures、error taxonomy、manifest 和四仓库 mirror gate；
- Fluxion、Approver、Bids owner command adapter 基座；
- Fluxion `project.annotate` 首个完整 command；
- Fluxion → Approver 低置信度派单审批试点；
- Record Hub receipt 查询、终态推进、冲突/DLQ 运维证据；
- 四应用 + MongoDB + NATS + Dex + workload issuer + Temporal + Conductor 隔离验收；
- 重启、ACK 丢失、积压恢复、凭据轮换、备份恢复和回滚计划。

### 2.2 延期

- Bids 高敏生命周期审批、投标/报价/文件/定标/合同/付款写入；
- 任意 Record Hub 表格字段自动映射为 owner command；
- 跨系统强一致事务、通用 Saga 编排器；
- 生产多地域、自动扩缩容和生产 IdP 最终选型；
- 将 owner 领域 SDK 或 Approver 实现包嵌入其他业务系统。

## 3. 共享契约需求（P3-CON）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-CON-001 | Command v1 schema | 明确字段、大小、格式、未知字段、canonical hash 和示例；四仓库逐文件 SHA-256 一致 |
| P3-CON-002 | Result v1 schema | 仅允许四种终态、安全错误和有界结果元数据；同 event ID 内容不可变 |
| P3-CON-003 | 错误分类 | 至少包含 validation、not-found、version-conflict、policy、idempotency-conflict、business-rejected、unavailable、internal-safe |
| P3-CON-004 | 兼容规则 | v1 additive-only；breaking 变化发布 v2 和新 subject/consumer，不原地改变语义 |
| P3-CON-005 | SDK/facade | Java/Kotlin、Go 调用方只依赖公共 schema/OpenAPI/facade，不导入 Record Hub 内部包 |
| P3-CON-006 | 契约供应链 | manifest 包含 schema、fixture、error 文件 hash；CI 拒绝缺失、漂移和未知 artifact |

## 4. Owner Command 需求（P3-CMD）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-CMD-001 | Durable consumer | 每个 owner 使用预创建的专属 pull consumer、explicit ACK、MaxDeliver、backoff 和 DLQ |
| P3-CMD-002 | 原生 Inbox | Inbox 位于 owner PostgreSQL；operation ID 唯一，保存 payload hash、状态和原结果 |
| P3-CMD-003 | 原子处理 | Inbox claim/终态、领域变更、审计和 result Outbox 在同一 owner transaction 提交 |
| P3-CMD-004 | 幂等 replay | 相同 operation ID/hash 返回原结果且不重复副作用；不同 hash 返回冲突 |
| P3-CMD-005 | expected version | owner 在行锁内比较当前 aggregate version；过期版本稳定 `REJECTED`，不得自动覆盖 |
| P3-CMD-006 | Result Outbox | transaction 提交后异步发布；发布确认前不标记 SENT；租约过期可由其他 worker 接管 |
| P3-CMD-007 | ACK 顺序 | 仅在 owner transaction 提交后 ACK；进程在 commit 后 ACK 前崩溃时重投返回原结果 |
| P3-CMD-008 | Receipt 终态 | Record Hub 以 operation revision CAS 推进；相同结果 replay，无关 owner/action 或不同终态拒绝 |
| P3-CMD-009 | Payload 安全 | command/result 最大 256 KiB、合法 UTF-8、严格 JSON；日志/DLQ 不包含原始 payload |
| P3-CMD-010 | 管理恢复 | retry/replay 需要 operator、原因和幂等键；禁止直接修改 Inbox、Outbox 或 receipt 状态 |

## 5. Fluxion 首个命令需求（P3-FLX-CMD）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-FLX-CMD-001 | `project.annotate` policy | 仅一个测试 tenant/workspace、`PROJECT`、`project-annotation` purpose 和精确 workload principal 可调用 |
| P3-FLX-CMD-002 | APPEND | 最多 500 字内部说明；不接受客户、地址、金额、人员或任意嵌套字段 |
| P3-FLX-CMD-003 | VOID 补偿 | 只追加 void 事件并引用同项目原 annotation；不物理删除审计历史 |
| P3-FLX-CMD-004 | 事务一致性 | annotation、summary version +1、summary Outbox、Inbox 终态和 result Outbox 同事务 |
| P3-FLX-CMD-005 | 状态保护 | 不改变派单、签到、验收、付款、补偿或 Temporal stage；非法/终态项目按契约拒绝 |
| P3-FLX-CMD-006 | 真实投影闭环 | 成功后 Fluxion summary event 推进 Record Hub source version，receipt resultVersion 与 owner version 对齐 |

## 6. Approver 审批试点需求（P3-APR）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-APR-001 | 独立服务 connector | Fluxion 通过 Approver integration API/SDK 调用；不导入领域实现，不创建跨系统 child workflow |
| P3-APR-002 | 稳定申请 | external request ID、Project ref、dispatch generation、proposal hash、workflow/run ref 和 requester identity 固定 |
| P3-APR-003 | Fluxion request Outbox | Activity 只写本地 request/outbox；HTTP sender 至少一次投递，响应丢失以原 ID 查询 receipt |
| P3-APR-004 | Approver contract handler | 使用 generic registry/materializer/result dispatcher；unknown/duplicate handler fail closed |
| P3-APR-005 | Immutable snapshot | Approver Application 保存有界、低敏派单摘要和 hash，不复制客户、地址或完整候选人档案 |
| P3-APR-006 | 结果 Inbox | Fluxion 按 external request ID + decision version 去重；同版本不同 hash 冲突 |
| P3-APR-007 | Workflow 恢复 | result transaction 提交后由本地 Outbox 调用 Temporal update；重复 update 不重复推进 |
| P3-APR-008 | 五类终态 | approved、rejected、withdrawn/cancelled、expired、failed 均有明确分支和 live E2E |
| P3-APR-009 | 晚到结果 | Project 已改派、取消、终止或 generation 变化时拒绝 Apply，生成 reconciliation finding，不回滚新事实 |
| P3-APR-010 | 灰度/回滚 | tenant/workspace feature flag 默认关闭；单 generation 不双写；关闭后不遗弃既有申请 |

## 7. Approver 与 Bids Owner Adapter 需求（P3-OWN）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-OWN-001 | Approver adapter | `application.annotate` 只追加 integration note；不改变 Application/Process/Task 决定 |
| P3-OWN-002 | Bids adapter | `tender.annotate` 只追加内部审计说明；不暴露或改变报价、投标、文件、定标、合同、付款 |
| P3-OWN-003 | 原生迁移 | Approver 使用 Flyway，Fluxion 使用 Flyway/Exposed，Bids 使用版本化 SQL/GORM；从空库和升级库均通过 |
| P3-OWN-004 | 租户隔离 | Approver tenant、Fluxion workspace、Bids organization 均由服务端映射；请求 payload 不能覆盖 |
| P3-OWN-005 | 版本语义 | 每个 owner 明确唯一 aggregate version；结果 version 与随后 domain summary event 单调一致 |
| P3-OWN-006 | 共享行为测试 | 三个 adapter 运行同一组 duplicate/hash-conflict/version-conflict/commit-ACK-loss/restart fixture |

## 8. 身份与安全需求（P3-SEC）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-SEC-001 | Human OIDC | 本地 Dex 四个 Web client 保持独立；浏览器会话不能调用机器 API |
| P3-SEC-002 | Workload identity | 每个信任方向独立 client、短 TTL、精确 audience/scope；不复用 Dex 用户 token、NATS 或 DB credential |
| P3-SEC-003 | 双向身份 | Fluxion→Approver 与 Approver→Fluxion 使用不同 principal/secret；Record Hub 六个方向同理 |
| P3-SEC-004 | NATS subject ACL | owner 只能读自己的 durable 和发布自己的 events/results；Record Hub 不能订阅未授权原始业务 subject |
| P3-SEC-005 | Secret rotation | 新旧 credential 有界重叠，旧版本过期后 fail closed；轮换不产生重复命令或丢失结果 |
| P3-SEC-006 | 数据最小化 | history、日志、metrics、DLQ、receipt 只含安全摘要；安全扫描不发现 token、cookie、报价、PII 或文件 URL |

## 9. Workflow 与一致性需求（P3-WF）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-WF-001 | Temporal deterministic boundary | 所有外部 I/O 位于 Activity/relay；Workflow 仅保存稳定 ID、version、hash 和安全结果 |
| P3-WF-002 | Continue-As-New | pending command/approval ID 被携带，或边界前证明不存在未完成外部操作 |
| P3-WF-003 | Conductor boundary | Bids Worker task 执行 I/O；result 先落 Bids Inbox，再由 Outbox 完成 HUMAN/SIMPLE task |
| P3-WF-004 | Timeout/fallback | owner/Approver 不可用有明确等待、重试、人工兜底和过期分支，不无限挂起 |
| P3-WF-005 | Replay | Temporal replay 不重新发 HTTP/NATS；Conductor task retry 复用原 operation/request ID |
| P3-WF-006 | 无跨系统 ACID 宣称 | 文档、API、UI 和验收报告统一描述为 at-least-once + idempotent processing |

## 10. 运维与验收需求（P3-OPS）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P3-OPS-001 | 隔离全拓扑 | 原生启动 Mongo/NATS/Dex/workload issuer/Temporal/Conductor/四应用；端口、数据库、日志和 PID 独立 |
| P3-OPS-002 | 有界证据 | 每次运行保存版本、commit、配置摘要、测试结果和截断日志；不得保存 secret |
| P3-OPS-003 | 故障注入 | command/result ACK loss、NATS outage、owner restart、Record Hub restart、Approver timeout/result loss 均可重复执行 |
| P3-OPS-004 | 对账 | operation、owner Inbox、result Outbox、Approver request/result、Workflow 状态不一致可发现并分类 |
| P3-OPS-005 | 指标/SLO | command terminal p95、oldest backlog、dead count、approval materialization/result latency 和 recovery time 可测 |
| P3-OPS-006 | 备份恢复 | 恢复后 unique/index/receipt/Inbox/Outbox/cursor/hash/count 一致，不重放已完成领域副作用 |
| P3-OPS-007 | 升级回滚 | contract v1、DB expand/contract、consumer durable、feature flag 和旧 worker 兼容窗口有演练 |
| P3-OPS-008 | Beta 报告 | PASS/SKIPPED/FAIL、风险 owner、重试条件、RPO/RTO 和容量数据完整，禁止用单测替代 live 项 |

## 11. 非功能基线

- command/result 最大 256 KiB；审批请求按 connector 设置更小的 allowlist 上限。
- 同步 HTTP 默认 timeout 不超过 10 秒；Workflow Activity retry 有最大次数和总时长。
- Inbox/Outbox claim 使用有界 batch、lease 和 backoff；进程关闭执行 bounded drain。
- 本地单节点仅用于功能验收，不作为 HA、RPO 或 RTO 证明。
- 所有新 PostgreSQL 表包含 tenant/organization scope、唯一约束、查询索引和保留策略。
- metrics label 只能使用 system/action/status/error-class 等有限枚举。
- 生产启用前必须重新评审 TLS、NATS account/NKey、正式 workload issuer、数据库备份和密钥管理。

## 12. 发布门

| Gate | 进入条件 | 退出条件 |
| --- | --- | --- |
| G0 契约冻结 | 本文与技术方案评审通过 | 四仓库 mirror/hash gate 通过 |
| G1 Fluxion command | NATS durable/ACL 和 DB migration 就绪 | 全部 command live 故障矩阵通过 |
| G2 Approver pilot | G1 通过，feature flag 默认关闭 | 五类终态、晚到结果、重启和回滚通过 |
| G3 三 owner adapter | G2 通过，共享 fixture 稳定 | Approver/Fluxion/Bids 行为矩阵一致 |
| G4 Beta readiness | 全拓扑可重复启动 | 容量、恢复、安全、升级报告无未解释高风险项 |

任一 Gate 未通过不得通过扩大 policy、增加高敏 command 或取消 fallback 来绕过。
