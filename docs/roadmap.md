# 实施路线

## Phase 0：契约与本地基线

- 建立 Go 1.27 模块、配置、健康检查和测试骨架。
- 建立 OpenAPI-first API 与生成式 Java/Go/TypeScript client 基线。
- 冻结术语、事实所有权和非目标。
- 启动本地 MongoDB replica set、NATS JetStream 和 Dex。
- 建立 event envelope、Schema 发布和兼容性测试。
- 为四个 Web 应用注册独立 Dex client。
- 为首批服务调用方向注册独立机器 client。

验收：四个系统能验证同一 issuer 的用户 token；两个示例服务能使用独立 client credentials；NATS subject 权限互相隔离。

## Phase 1：只读投影 MVP

- Schema Registry。
- Record、tag、relation、view 最小 API。
- Approver、Fluxion、Bids 各选择一种低敏资源发布事件。
- Durable projector、Inbox 去重、gap 检测和 DLQ。
- UI 展示 source version、syncedAt 和 projection lag。

首批建议资源：

- Approver：Application 安全摘要；
- Fluxion：Project 当前阶段；
- Bids：Tender 公共状态，不含投标内容和报价。

## Phase 2：Workflow 只读 Binding

- Snapshot API、canonical hash 和稳定 record reference。
- Temporal Activity adapter。
- Conductor Worker adapter。
- timeout/retry/conflict/replay 测试。

验收：Workflow history 只保存引用和 hash；记录变化不会改变已启动节点的 snapshot。

## Phase 3：受控命令

- Command Gateway、operation receipt 和 owner-system Inbox。
- expected version、冲突和 Saga 策略。
- 将一个低风险操作从表格路由回 owner system。
- 禁止直接编辑外部状态字段。

## Phase 4：审批集成

- Record Hub 作为审批上下文投影，不取代 Approver Application snapshot。
- Fluxion/Bids 审批 request/result 通过既有 Approver connector 基座。
- 表格提供审批关联和状态速查，但审批动作只在 Approver 执行。

## Phase 5：生产准入

- 多租户、行列字段授权和 Bids 密封数据负向测试。
- JetStream 集群故障、重放、积压和容量测试。
- MongoDB transaction、备份恢复和索引容量测试。
- Dex issuer、JWKS、client rotation 和上游 IdP 故障测试。
- Outbox/Inbox、重复、乱序、gap、响应丢失和跨进程重启测试。
- Runbook、指标、告警、RPO/RTO 和审计签字。
