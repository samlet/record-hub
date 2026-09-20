# 实施路线

本文件描述按能力递进的长期路线，不等同于发布版本号。MVP 已在完成 Phase 0-2 核心能力后
有条件收口；下一发布周期的取舍、需求和批次见[下一期方案评估](phase-2-evaluation.md)与
[下一期需求设计](phase-2-requirements.md)。Phase 3 已完成首批真实系统接入和审批试点；Phase 4
将收口其 live 缺口，并把独立服务 connector 提升为受限租户可运行的 Integration Beta。

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

真实系统接入的方案、需求、任务与验收见
[Phase 3 接入方案](phase-3-integration-design.md)、[需求](phase-3-requirements.md)、
[任务分解](phase-3-task-breakdown.md)和[验收计划](phase-3-acceptance-plan.md)。

- Command Gateway、operation receipt 和 owner-system Inbox。
- expected version、冲突和 Saga 策略。
- 将一个低风险操作从表格路由回 owner system。
- 禁止直接编辑外部状态字段。

## Phase 4：Integration Beta

详细边界和发布门见 [Phase 4 技术方案](phase-4-design.md)、
[需求](phase-4-requirements.md)、[任务分解](phase-4-task-breakdown.md)和
[验收计划](phase-4-acceptance-plan.md)。

- 收口 Phase 3 的 credential rotation、备份恢复、混合容量和升级回滚 live gate。
- 将 Fluxion → Approver 试点提升为可灰度、可对账、可降级的 Beta 集成。
- 为 Bids 增加一条不含密封投标、报价、合同或付款正文的低风险 Approver 审批切片。
- Record Hub 提供审批关联、状态速查和 SLO 读模型，但不取代 Approver Application snapshot。
- 完成多租户负向矩阵、真实 RPO/RTO、rolling rollback 和 Beta 验收报告。

## Phase 5：生产 GA 与规模化

Phase 5 的方案、需求、任务和验收见 [Phase 5 技术方案](phase-5-design.md)、[需求基线](phase-5-requirements.md)、[任务分解](phase-5-task-breakdown.md) 和 [验收计划](phase-5-acceptance-plan.md)。

- 正式 workload issuer、TLS、secret manager、HA 数据层和生产备份策略。
- 集群级故障切换、容量扩展、长期 retention 和灾备演练。
- connector 生命周期、兼容窗口、发布治理和自助接入规范。
- 正式安全/合规/运维签字；依据实际需求评估多地域，而不预先承诺。

## Phase 6：真实业务扩展与自助化数据平面

Phase 6 的方案、需求、任务和验收见 [Phase 6 技术方案](phase-6-design.md)、[需求基线](phase-6-requirements.md)、
[任务分解](phase-6-task-breakdown.md) 和 [验收计划](phase-6-acceptance-plan.md)。

- 在 Phase 5 GA 证据完成后，扩展 Approver、Fluxion、Bids、Settlement 的真实 connector。
- 增强 schema.org 语义、多维表格 tags/relations/views、redacted export 和 workflow binding。
- 以 registry-backed NATS event/replay、Temporal/Conductor typed reference 和自助化 control plane 为重点。
- 不默认承诺多地域 active-active、零 RPO 或跨 owner 分布式事务。
