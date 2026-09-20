# Record Hub Phase 6 技术方案：真实业务扩展与自助化数据平面

- 状态：Proposed / 方案评估基线
- 前置：Phase 5 的 P4-501、P5-G1～G7 必须全部 live `PASS`；未满足时只允许设计和 contract 工作
- 关联系统：Approver、Fluxion、Bids、Settlement、Temporal、Conductor、Dex、MongoDB、NATS JetStream
- 目标：在不改变事实所有权的前提下，把已验证的中间件能力扩展为可复用的真实业务 connector、
  多维表格和 workflow-native event/data platform

## 1. 阶段定位

Phase 6 不重新解决 Phase 5 的 HA、灾备或 GA 放行问题，也不把 Record Hub 变成任意 workflow engine。
它解决的是 Production GA 之后的业务扩展：让 Approver、Fluxion、Bids 能以统一 connector contract
接入，让用户可以在受控 schema 下使用 tags、relations、views 和状态速查，并让 Temporal/Conductor
以稳定引用调用数据，而不是把完整业务 payload 写进 workflow history。

Phase 6 的第一原则仍然是：业务事实由 owner 保有，Record Hub 只保有自己的记录、协作字段、安全投影、
schema、关联、receipt、finding 和审计索引。

## 2. 架构决策

### ADR-P6-001：connector adapter 优先，禁止跨仓库包耦合

Approver、Fluxion、Bids 通过版本化 HTTP/NATS contract、SDK 和 Inbox/Outbox 接入。不得导入另一个
业务系统的内部包，也不把 Approver 启动为其他系统的 child workflow。Temporal/Conductor 仍由各业务
系统拥有，Record Hub 只提供 command、receipt、snapshot 和 projection API。

### ADR-P6-002：schema.org 是语义参考，不是外部 schema 的权威

多维表格 schema 可以引用 schema.org IRI、`typeOf` 和受控 property set；实际字段、约束、版本和
兼容窗口由 Record Hub Schema Registry 冻结。未知 IRI、任意远程 schema fetch 和未注册继承关系全部
fail closed。

### ADR-P6-003：MongoDB 保存 Record Hub 记录，NATS JetStream 承担可重放事件

MongoDB 负责记录、schema、tag、relation、view、projection pointer 和 reconciliation finding；NATS
负责 allowlisted domain event、command/result 和 DLQ。Mongo 不是事件总线，NATS 也不是业务事实库。

### ADR-P6-004：表格引用的事务属性只表达边界，不提供分布式事务

结点对表格的引用可以声明 `READ_SNAPSHOT`、`READ_LATEST`、`COMMAND_RECEIPT` 或
`OWNER_APPLY_REQUIRED`。这些属性用于 workflow activity、审计和重试语义；不承诺 Mongo、PostgreSQL、
NATS、Temporal、Conductor 之间的 XA/2PC 或 exactly-once。

### ADR-P6-005：Human 与 Workload identity 继续分离

Dex/OIDC 负责用户登录、membership 和角色；每个 owner 与 Record Hub 使用独立 workload principal、
audience、scope、密钥轮换和撤销。表格 UI 不得直接持有 owner 数据库凭据或任意 NATS publish 权限。

## 3. 目标架构

```text
Dex/OIDC ── human claims ──> Record Hub Console/API
                                ├─ MongoDB: records/schema/tags/relations/views
                                ├─ NATS JetStream: allowlisted events/commands/results/DLQ
                                ├─ Connector Registry + SDK compatibility gate
                                ├─ projection/reconciliation/finding workers
                                └─ workflow binding API: snapshot/ref/receipt

Approver / Fluxion / Bids ── owner adapters ──> Registry + Command Gateway
        │                                           │
        └── own Temporal/Conductor + domain DB <────┘
```

## 4. 业务扩展顺序

1. **Approver**：Application、Process、Task 的安全摘要和审批关联先完成；Settlement confirmation 仍
   由 Approver/Settlement 直接 Apply，Record Hub 只投影安全摘要和 receipt。
2. **Fluxion**：Project 阶段、低置信度派单审批和审批结果 reconciliation；保留 local fallback，按
   tenant/workspace 灰度。
3. **Bids**：先做招标准备发布审批，只传稳定引用、计划时间、组织引用、schema/version/hash；不接触
   密封投标、报价、评审打分、合同或付款正文。
4. **通用 connector**：当三方切片都通过同一 contract、故障、redaction 和 rollback 矩阵后，才开放
   self-service registration；未知 connector 不自动 fallback。

## 5. 多维表格能力边界

- `Table`、`Field`、`Record`、`Tag`、`Relation`、`View`、`SchemaVersion` 均有 tenant/workspace scope。
- schema 采用显式字段类型、JSON Schema 约束、schema.org 语义引用和继承版本；不能按记录动态改类型。
- tags 是受控字典，可设置颜色、归属、互斥组和审计；不能把 tag 当权限或事实状态。
- relation 使用稳定 typed ref、source version 和可选 cardinality；跨 owner relation 只保存安全摘要。
- view 是查询定义和投影 pointer，不复制隐私字段；导出必须通过 redaction policy。
- `OWNER_APPLY_REQUIRED` 的表格引用只生成 command receipt，不能直接修改 owner 终态。

## 6. 发布和退出条件

每个新 connector 或 schema family 必须提供 owner、scope、contract/schema、fixture、migration、
redaction、兼容窗口、rollback、replay、late-result 和故障矩阵。任何 scope 泄漏、重复领域副作用、
敏感字段进入 log/metric/DLQ/evidence、schema 破坏性变更或无法回滚，立即停止该 connector 的新流量，
保留旧 worker/fallback 并进入 reconciliation。

Phase 6 不默认承诺多地域 active-active、零 RPO、任意脚本、任意 NATS subject、自动跨 owner 补偿或
Supabase 全功能兼容。
