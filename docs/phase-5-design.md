# Record Hub Phase 5 技术方案：Production GA 与规模化

- 状态：Proposed / 需求评估基线
- 前置：Phase 4 Batch 5；P4-501 live gate 必须先完成
- 关联系统：Approver Phase 5（Settlement confirmation）、Settlement Phase 6、Fluxion、Bids
- 目标：把当前 Integration Beta 提升为可运营、可恢复、可审计的单地域 Production GA 基线

## 1. 阶段边界

Phase 5 不新增任意业务审批或财务事实，而是把已验证的 Record Hub data plane、独立服务身份、NATS JetStream 事件总线、MongoDB 投影和多维表格能力生产化。Approver/Fluxion/Bids/Settlement 继续拥有各自领域状态；Temporal、Conductor 和各业务 Workflow 仍由业务系统负责。

Record Hub 只保存安全投影、关联、schema、命令 receipt、evidence 和 bounded finding，不成为 Approver Application、Fluxion Project、Bids Tender 或 Settlement 的事实权威，也不提供直接修改外部终态的入口。

## 2. 架构决策

### ADR-P5-001：单地域 HA 优先，多地域不默认承诺

MongoDB 使用生产 replica set、备份/PITR 和定期 restore drill；NATS 使用 JetStream 多副本 stream、consumer durable 和明确的 storage/retention；应用采用至少两副本和受控滚动发布。多地域 active-active、自动灾备切换和零 RPO 不属于本期默认承诺。

### ADR-P5-002：Dex 只负责用户身份，机器身份独立

Dex/OIDC 提供 Web 用户登录和 workspace membership 的身份来源。四个 workflow 系统与 Record Hub 的服务调用使用独立 audience、scope、credential/certificate 和 rotation policy，凭据进入 production secret manager，不通过浏览器 Session、Partner API key 或 `.env` 共享。

### ADR-P5-003：Connector contract registry 是唯一接入扩展点

connector、event type、schema version、result handler、reconciliation gateway 和 allowed fields 由 server-side registry 管理；未知 key fail closed。接入不能由表单、workflow 变量或请求 body 提供 endpoint、tenant、credential、process key 或任意 action。

### ADR-P5-004：事件至少一次，领域副作用最多一次

Inbox/Outbox、JetStream redelivery、Temporal replay 和 Conductor retry 都允许至少一次。稳定 request/event/idempotency key、payload hash、expected version 和 owner-side Apply 保证重复消息不重复领域副作用；Record Hub 不使用 XA/2PC。

### ADR-P5-005：Settlement approval 保持三方边界

Approver Phase 5 的 Settlement confirmation 与 Settlement Phase 6 的 Apply 由两方直接完成。Record Hub 只投影安全关联、receipt、result 和 reconciliation finding；不复制金额/银行/发票附件，不执行确认或付款。

## 3. 目标拓扑

```text
Dex/OIDC ──用户身份/claims──┐
                            ▼
                    Record Hub API/BFF (>=2)
                    ├─ MongoDB replica set (>=3)
                    ├─ NATS JetStream (>=3)
                    ├─ Schema/contract registry
                    ├─ projection + command workers
                    └─ evidence/finding/reconciliation views

Approver/Fluxion/Bids/Settlement ──独立 service principals──► Record Hub
Approver/Fluxion/Bids/Settlement 各自拥有 Temporal/Conductor workflow 与领域数据库
```

## 4. 生产化能力

### 4.1 Identity、tenant 和 policy control plane

- tenant、organization、workspace、identity、service principal 和 connector mapping 有显式生命周期、审计和版本。
- workspace membership 的 Viewer/Editor/Operator/Admin 权限在 API、projection、command、operations 和 console 一致执行。
- tenant/org/resource 任一 scope 不匹配均 fail closed，不能用 404/响应差异泄露存在性。
- service principal 只获最小 audience/scope；每个 owner 的 inbound/outbound 方向独立凭据。

### 4.2 Data/Event plane

- Mongo indexes、TTL/retention、archive、backup、restore 和 schema migration 可版本化；projection rebuild 使用 staging/read pointer/CAS。
- JetStream stream/consumer 的 subject、durable、ack、max delivery、DLQ 和 retention 固定于 deployment manifest。
- Inbox/Outbox lease、attempt、next retry、dead 和 reconciliation finding 可观察、可重放、可审计。
- 所有跨系统 payload 使用 schema/manifest/hash；敏感字段只在 owner side 保留。

### 4.3 Connector 与 Settlement pilot 生产化

- Fluxion dispatch approval 和 Bids tender publication approval 进入 connector lifecycle：注册、兼容窗口、禁用、回滚、deprecated 和 owner。
- Settlement confirmation 只允许安全摘要、snapshot hash、版本和审批关联；确认/付款仍由 Settlement Apply 事务执行。
- Connector SDK 固定 semver 和 compatibility matrix；未知版本进入 quarantine/finding，不自动降级到另一个 connector。

### 4.4 运维、可观测性与合规

- OTel trace 与 bounded metrics 贯穿 intake、materialization、delivery、projection lag、backlog、DLQ、finding、recovery 和 terminal outcome；label 禁止 tenant/user/resource/request/hash。
- 定义 p95/p99、availability、lag、backlog、delivery retry/dead、RPO/RTO、restore 和 rotation 告警阈值。
- evidence manifest、redaction、retention、export checksum 和 operator audit 可独立验证。
- 安全扫描覆盖 Git、history、日志、DLQ、metrics、backup/evidence 和构建 artifact；production secret 不进入 repository。

## 5. 发布策略

先完成 P4-501 live gate，再以一个低敏 tenant/workspace 灰度；观察完整 retry/retention/reconciliation 窗口后扩大范围。旧 worker、旧 schema reader、Fluxion local fallback 和 rollback artifact 在 Phase 5 首个生产窗口内保留，删除需单独评审。

## 6. 非目标

- 本期不做多地域 active-active、零 RPO、自动跨地域 failover。
- 不把 Record Hub 变成通用 workflow engine、Approver 替代品或财务/招标事实数据库。
- 不允许任意表格写入外部系统终态，不允许通过 Mongo/NATS 直写绕过 owner API。
- 不一次性迁移所有 Approver/Fluxion/Bids 审批；只推进已冻结契约和 Settlement confirmation 切片。
