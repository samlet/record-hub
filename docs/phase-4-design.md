# Phase 4 Integration Beta 技术方案

- 日期：2026-09-20
- 状态：Proposed
- 上游基线：[Phase 3 验收报告](phase-3-acceptance-report.md)
- 覆盖仓库：Record Hub、Approver、Fluxion、Bids
- 阶段定位：真实业务集成收口与 Production-like Beta 准入，不是生产 GA

本文件中的 Phase 4 指 Record Hub 跨仓库集成计划，不等同于 Approver、Fluxion 或 Bids 各自内部的
同名阶段。

## 1. 结论

Phase 4 建议定位为 **Integration Beta：在受限租户、受限动作和可回滚条件下运行真实业务集成**。
它不再以增加 Record Hub 平台能力为主，而是完成以下闭环：

1. 收口 Phase 3 中仍为 `SKIPPED`/`PARTIAL` 的凭据轮换、备份恢复、混合容量和升级回滚门禁；
2. 将 Fluxion → Approver 审批试点提升为可灰度、可对账、可降级的真实集成；
3. 为 Bids 增加一条不含密封投标、报价、合同或付款正文的低风险 Approver 审批切片；
4. 让 Record Hub 提供跨系统审批关联、状态速查、审计索引和 SLO 观测，但不成为审批事实库；
5. 形成有版本、有证据、有 RPO/RTO、有回滚结论的 Beta 发布报告。

Phase 4 完成不等于生产 GA。多地域、高可用、生产密钥管理、正式合规签字和规模化 connector
生态留给 Phase 5。

## 2. 当前基线与缺口

Phase 3 已证明四个真实应用可在原生隔离拓扑中启动，并完成 Temporal/Conductor workflow、NATS
outage/recovery 和 restart/ACK-loss 验收。但以下结论尚未成立：

| 基线项 | 当前状态 | Phase 4 处理 |
| --- | --- | --- |
| workload/JWKS/secret 热轮换 | P3-405 `SKIPPED` | 建立双 key/secret overlap、reload 与撤销窗口 |
| Mongo/PostgreSQL/JetStream 恢复 | P3-406 `SKIPPED` | 在隔离 source/restore targets 上完成真实恢复与一致性断言 |
| 四 owner 混合容量 | P3-407 `PARTIAL` | 固定硬件、数据量、事件率、并发与 backlog recovery SLO |
| rolling upgrade/rollback | P3-408 `SKIPPED` | 保存 old/new artifacts，演练 expand/contract 与 feature flag rollback |
| 三 owner live matrix | P3-308 `SKIPPED` | 补齐 Approver、Fluxion、Bids 同时运行的领域副作用矩阵 |
| Fluxion command 深层故障边界 | P3-110 `PARTIAL` | 补齐 transaction rollback、publish-before-SENT crash |

这些项目属于 Phase 4 的进入门，不得通过改名、降低断言或只跑单元测试转成 `DONE`。

## 3. 目标与非目标

### 3.1 目标

- 单一 Beta tenant/workspace 可以同时使用 Record Hub、Approver、Fluxion 和 Bids。
- 每条跨系统请求都有稳定 `operationId` 或 `externalRequestId`，可从 API、Outbox、Inbox、workflow
  和审计记录追踪到终态。
- 所有业务副作用仍在 owner 数据库事务中完成；消息重复、响应丢失、进程重启不产生重复副作用。
- 审批 connector 支持默认关闭、按 tenant 灰度、快速停止新流量和保留在途请求。
- 凭据、恢复、容量、升级和对账均有真实 live evidence；证据不再只保存在系统临时目录。
- Beta 发布报告明确 PASS/SKIPPED/FAIL、风险 owner、版本、配置摘要、RPO/RTO 和回滚条件。

### 3.2 非目标

- 不实现跨 MongoDB/PostgreSQL/NATS/Temporal/Conductor 的 XA、2PC 或 exactly-once。
- 不把 Approver workflow 作为 Fluxion/Bids 的 child workflow，也不导入 Approver 内部包。
- 不允许 Record Hub 直接写 owner 业务表、完成 Conductor task 或 Signal/Update 业务 workflow。
- 不开放 Bids 定标、报价、密封投标文件、合同签署、付款等高敏 command。
- 不承诺多地域、零数据丢失、自动灾备切换或无限水平扩展。
- 不在本阶段追求 Supabase 全功能对标、任意用户脚本或任意 subject 的事件平台。

## 4. 目标架构与所有权

```text
Fluxion Temporal ──request outbox──> Approver integration API
       │                                      │
       │                         Application / Process / Decision
       │                                      │
       <──result inbox/outbox + reconciliation┘

Bids Conductor ───request outbox───> Approver integration API
       │                                      │
       <──result inbox/outbox + task completion

Approver / Fluxion / Bids domain outbox
       └──────────────> NATS JetStream ──> Record Hub projection
                                               │
                                      approval association/status
                                      operation/audit/SLO read model
```

事实所有权保持不变：

| 数据/动作 | Owner | Record Hub 角色 |
| --- | --- | --- |
| Application、Process、Task、审批决定 | Approver | 只读安全摘要与关联 |
| Project、派单与履约状态 | Fluxion | 只读投影、binding、受控 command receipt |
| Tender 与 Conductor task | Bids | 只读投影、binding、受控 command receipt |
| Schema、mapping、view、tag、协作字段 | Record Hub | 最终事实 |
| 跨系统 request/result delivery | 发起方与 Approver 各自 Inbox/Outbox | 状态索引和对账视图，不是消息事实库 |

Record Hub、Approver、Fluxion、Bids 继续以独立服务部署。跨系统一致性模型是
`at-least-once delivery + idempotent processing + reconciliation`。

## 5. 真实业务切片

### 5.1 Fluxion 低置信度派单审批

Phase 3 的试点在 Phase 4 中提升为 Beta 切片：

- Fluxion Temporal workflow 仍拥有履约状态和本地 human-task fallback；
- Activity 只写稳定 approval request 与 transaction outbox，不在 workflow deterministic code 中发 HTTP；
- Approver 以 `(tenantId, sourceSystem, externalRequestId)` 幂等创建不可变 Application；
- decision result 经 Approver result Outbox 发送，Fluxion Inbox 去重后再写 Temporal update Outbox；
- timeout、拒绝、取消、晚到结果和重复结果都有确定分支；关闭 feature flag 后不创建新请求，在途请求继续
  drain 或由 operator 显式取消；
- Record Hub 仅展示 Project ↔ Application 的稳定引用、安全状态、version 和 `syncedAt`。

### 5.2 Bids 招标准备发布审批

首条 Bids → Approver 切片选择“招标准备发布审批”，只传递 allowlist 元数据：

- tender/lot 稳定引用、标题或安全显示名、计划发布时间、组织引用、schema/version/hash；
- 不传递投标正文、报价、供应商密封文件、评审打分、定标意见、合同或付款信息；
- Approver 决定只表达 `APPROVED/REJECTED/CANCELLED/EXPIRED`，Bids 自己校验当前版本和业务前置条件；
- Bids result Inbox 与业务状态/Conductor task-completion Outbox 在同一 PostgreSQL 事务提交；
- Conductor retry 必须复用原 external request ID，不重复创建 Application 或完成 task。
- feature flag 按 organization/tender generation 在 external Approver 与现有 Bids HUMAN task 中二选一；
  同一 generation 不得创建两套可决定的审批任务，关闭 flag 后既有外部申请仍可 drain/cancel。

该切片通过故障、安全和回滚矩阵前，不扩展到定标、合同和付款审批。

### 5.3 Record Hub 审批关联视图

Record Hub 增加的是派生读模型，不是通用审批写入口：

- 使用稳定 typed refs 关联 source resource、Approver Application 和 workflow instance；
- 显示 source status、approval status、version、last synced、lag 和 reconciliation finding；
- 支持按 tenant/workspace、owner system、状态和时间范围查询；
- 不存审批意见正文、附件正文、密封字段或完整业务 payload；
- “去审批”操作跳转 Approver，决定动作仍由 Approver 权限和审计控制。

## 6. 身份、安全与租户边界

- Human 登录继续使用 Dex OIDC；Beta 必须启用 issuer、audience、tenant claim 和 tenant mapping 的
  fail-closed 校验。
- Workload identity 与 Human token 分离。Dex 只有在真实 client-credentials token、短 TTL、scope、
  双 key/secret rotation 和撤销行为全部通过门禁后才能作为 Beta workload issuer；否则使用独立
  OAuth 2.0/OIDC Authorization Server。
- 每个调用方向使用独立 client：`fluxion-to-approver`、`bids-to-approver`、各 owner 到 Record Hub；
  禁止共享 secret 或使用用户 token 代替服务身份。
- NATS account/NKey、MongoDB/PostgreSQL credential 与 OIDC token 分离，按 owner subject/database
  最小授权。
- 所有 API、Inbox、Outbox、projection 和 association key 都必须包含并校验 tenant/organization scope；
  跨租户 mismatch 直接拒绝且不暴露资源是否存在。
- 证据、日志、metrics 和 DLQ 只保存稳定 ID、hash、状态和 allowlist error class，不保存 token、PII、
  报价、文件 URL 或审批正文。

## 7. 可靠性、恢复和升级策略

### 7.1 请求与结果

- request/result 均先写本地 transaction outbox，再异步发送；收到方以稳定 ID 和 payload hash Inbox 去重。
- owner transaction 必须同时提交 Inbox 终态、领域事实和下一跳 Outbox，提交后才 ACK。
- 对账器分类 `MISSING_REQUEST`、`MISSING_RESULT`、`TERMINAL_CONFLICT`、`VERSION_DRIFT`、
  `WORKFLOW_NOT_UPDATED`，只建议重试/人工处置，不直接改业务状态。
- DEAD replay 保留原 event/request/operation ID，要求 operator、原因和新的管理命令 Idempotency-Key。

### 7.2 备份恢复

- MongoDB、三个 PostgreSQL owner 数据库和 JetStream store 使用同一带时间戳的 recovery set manifest；
- 恢复到隔离目标后重新导出并比较 count/hash/index/version、未完成 Inbox/Outbox、durable cursor；
- 恢复后先禁止外部写入，完成对账，再按 owner → NATS relay → Record Hub consumer 顺序恢复流量；
- Beta 暂定目标：RPO ≤ 15 分钟、RTO ≤ 60 分钟。它们必须由 live exercise 证明，否则保持
  `UNVERIFIED`，不能写入服务承诺。

### 7.3 升级回滚

采用 expand/contract：

1. 先发布 additive migration 和兼容 reader；
2. 发布新 producer/consumer，但保持 tenant feature flag 关闭；
3. 单 tenant 灰度，至少观察一个完整 retention/retry window；
4. 回滚时保留新增表/字段，旧 worker 必须忽略 additive 数据；
5. 禁用新流量后 drain/cancel 在途 operation/request；
6. 所有旧版本退出后才允许 contract 或 schema 收紧。

每次演练必须保存 immutable old/new artifacts、四仓库 commit、migration version、durable consumer
state 和 rollback 后对账结果。

## 8. 容量与 SLO 基线

Phase 4 使用可重复场景建立 Beta 基线，不从 P3-407 的 20-event synthetic 结果外推：

| 项目 | Beta 验收起点 |
| --- | --- |
| Inbox/Outbox 历史 | 每 owner 至少 10,000 条 |
| command/result | steady 50 msg/s、burst 200 msg/s |
| 并发审批等待 | 100 个 Fluxion/Bids request |
| projection lag | 暂定 p95 ≤ 5 秒、p99 ≤ 30 秒 |
| command terminal latency | 无故障场景暂定 p95 ≤ 5 秒 |
| backlog recovery | 停 consumer 10 分钟后，在记录硬件与 worker 并发下测得 drain time，并形成可接受阈值 |
| 资源记录 | CPU、RSS、Mongo/PostgreSQL storage、JetStream file store、连接数和 worker concurrency |

这些数值是 Beta 验收起点。若机器资源不足可以形成偏差报告，但不得把缩小规模的结果解释为生产容量。

## 9. 可观测性与证据

统一关联键为 `traceId / operationId / externalRequestId / eventId / tenantId / workspaceId /
ownerSystem / resourceRef / workflowId / runId / applicationId / processInstanceId`。

Phase 4 增加以下要求：

- command terminal、approval materialization、decision delivery、projection lag、oldest backlog、DEAD count、
  reconciliation finding 和 recovery time 均有有限基数指标；
- evidence 写入明确的运行目录并生成 manifest、SHA-256 和 redacted config，不再只依赖 `/tmp`；
- CI 或发布流水线保留 evidence artifact，默认至少覆盖一个 release/retry window；
- 每个 gate 输出机器可读 `PASS/SKIPPED/FAIL`，缺少 live 依赖只能是 `SKIPPED`；
- 告警必须指向 owner、runbook 和允许的恢复动作，不能提供“直接把状态改成功”的按钮。

## 10. 实施批次

| 批次 | 目标 | 退出条件 |
| --- | --- | --- |
| Batch 0：契约与证据基线 | 冻结 Phase 4 requirements、contract additions、evidence manifest 和四仓库 commit | 文档评审通过，镜像/hash gate 通过 |
| Batch 1：Phase 3 收口 | rotation、backup/restore、mixed capacity、upgrade/rollback、剩余 fault boundaries | P3-405/406/408 转为真实 PASS，P3-407 完成四 owner baseline，P3-308/110 缺口有结论 |
| Batch 2：Fluxion Approval Beta | tenant 灰度、在途 drain、晚到结果、fallback、reconciliation | 五类终态与故障矩阵通过，关闭 flag 可安全回退 |
| Batch 3：Bids Approval Pilot | 招标准备发布审批 request/result/Conductor completion | 不含高敏字段；duplicate/restart/timeout/version conflict 全通过 |
| Batch 4：运维与安全 | tenant negative matrix、rotation、恢复、审计、SLO/告警 | 无跨租户、敏感泄漏、不可恢复 migration 或未解释数据丢失 |
| Batch 5：Beta 发布 | old/new rolling exercise、灰度运行、最终报告 | 所有必选 gate PASS；风险、owner、RPO/RTO、rollback 明确签字 |

Batch 1 是后续真实流量的硬前置。若某项因环境缺失为 `SKIPPED`，可以继续开发后续代码，但不能进入
Batch 5 的 Beta 发布判定。

## 11. Gate 与发布判定

| Gate | 必须通过 |
| --- | --- |
| P4-G0 Contract | 四仓库 contract mirror/hash、兼容性与 fixture |
| P4-G1 Safety | tenant isolation、scope、field allowlist、Bids sealed-data negative tests |
| P4-G2 Reliability | duplicate、ACK loss、response loss、restart、outage、late result、reconciliation |
| P4-G3 Recovery | Mongo/PostgreSQL/JetStream 恢复及 RPO/RTO 实测 |
| P4-G4 Capacity | 四 owner mixed load、SLO、backlog drain 和资源记录 |
| P4-G5 Release | credential rotation、rolling upgrade/rollback、feature flag/fallback、Beta report |

以下任一情况直接阻止 Beta 发布：

- 数据丢失或重复领域副作用无法解释；
- 跨租户访问、密封数据或敏感正文进入 Record Hub/NATS/evidence；
- 恢复后 index/version/cursor 不一致；
- 旧 worker 无法安全回滚，或关闭 feature flag 会遗留不可管理的在途请求；
- 必选 live gate 仍为 `SKIPPED`/`PARTIAL`。

## 12. 风险与控制

| 风险 | 控制 |
| --- | --- |
| 四仓库版本漂移 | release manifest 固定 commit/artifact checksum；contract mirror gate 在每仓库运行 |
| Approver 与 owner 双重状态机漂移 | 明确事实所有权、stable IDs、result Inbox、定期 reconciliation |
| 本地 Dex 被误当生产身份系统 | local/Beta/production 配置分层；Beta workload issuer 必须先通过 rotation/revocation gate |
| Bids 高敏数据泄漏 | 固定 allowlist schema、negative fixtures、payload size/hash、日志与 DLQ 扫描 |
| 临时证据丢失 | evidence artifact retention、manifest hash、redacted config 和 release report |
| 容量结论被过度外推 | 固定硬件/规模/并发，报告明确适用范围和偏差 |
| fallback 长期不清理或过早删除 | 由 P4-G5 决定清理窗口；Beta 期间保留旧 worker/schema reader/local fallback |

## 13. 后续文档与决策顺序

本方案评审后依次产出：

1. [Phase 4 需求](phase-4-requirements.md)：把目标拆成可验证需求和安全负向要求；
2. [Phase 4 任务分解](phase-4-task-breakdown.md)：按四仓库、依赖、批次和 commit gate 分解任务；
3. [Phase 4 验收计划](phase-4-acceptance-plan.md)：定义 live topology、fault matrix、容量、恢复和发布证据；
4. 每批独立实现、验证、提交；最终形成 `phase-4-acceptance-report.md`。

需要在需求冻结前确认的参数只有 Beta 部署规模、evidence 保留期、RPO/RTO 目标和正式 workload
issuer 选择。它们未确认时可以保留为 `TBD`，但不得用本地默认值替代发布承诺。
