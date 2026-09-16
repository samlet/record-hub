# 下一期方案评估：Integration Beta

日期：2026-09-17

## 1. 结论

下一期建议定位为 **Integration Beta：可运维的 Workflow 数据与命令集成平台**，而不是立即
扩张为通用 Supabase 替代品。

优先顺序是：

1. 解决 human identity 与 workload identity 的职责分离，并建立可重复的全拓扑验收环境；
2. 把 Schema、Projection、Binding 从“可用功能”提升为可迁移、可观测、可运营的产品能力；
3. 只为一个低风险动作引入 Command Gateway，验证 receipt、owner Inbox、冲突与补偿语义；
4. 在该基线之上接入 Approver，形成 Fluxion/Bids → Approver 的统一审批模式；
5. 浏览器实时订阅、Storage、Functions、Presence、GraphQL 和生产多地域继续延期。

该路线保留 Record Hub 的差异化：它是 workflow-native integration hub；MongoDB、NATS、
Dex 和 Workflow engine 是实现组件，不应被包装成“已经等价 Supabase”。

## 2. 备选方案

| 方案 | 价值 | 主要风险 | 结论 |
| --- | --- | --- | --- |
| A. 直接扩张为通用 BaaS | 功能面大，演示吸引力强 | Auth 生命周期、RLS、Realtime、Storage、Functions、备份与控制面同时展开，验收面失控 | 不选 |
| B. 只继续做只读投影 | 风险最低，沿用 MVP 边界 | 无法验证业务闭环，也无法回答 Approver 如何被 Fluxion/Bids 复用 | 不选 |
| C. 可运维基线 + 单一受控命令 + 审批集成 | 能关闭 MVP 的真实环境缺口，同时验证核心差异化 | 需要先冻结 machine identity 与 command ownership | **推荐** |
| D. 各业务系统直接嵌入 Approver 包或 child workflow | 初期代码少 | JVM/Go/引擎耦合、发布周期绑定、事实所有权混乱 | 不选 |

## 3. 推荐架构

```text
Browser ──OIDC──> Dex ──> Record Hub Web/API

Workflow/Service ──workload token or mTLS──> Record Hub API
Approver/Fluxion/Bids ──transactional outbox──> NATS JetStream
NATS JetStream ──durable projector──> MongoDB read models

Fluxion/Bids ──command request──> Record Hub Command Gateway
Record Hub ──NATS command + operation receipt──> owner-system Inbox
owner system ──domain transaction/outbox──> result event ──> Record Hub

Fluxion/Bids workflow ──Approver client/connector──> Approver service
Record Hub <──projection/events── Approver
```

边界：

- Dex 继续负责浏览器用户登录。除非其稳定版本明确支持并满足需求，否则不承担服务凭据签发。
- 服务身份独立评审 OAuth 2.0 Authorization Server、SPIFFE/mTLS 或云 workload identity；API
  只依赖标准 issuer/audience/subject/purpose contract。
- Record Hub 不直接写 Approver、Fluxion 或 Bids 数据库；命令只能由 owner-system Inbox 执行。
- Temporal/Conductor history 继续只保存稳定 reference、version 和 snapshot hash。
- NATS 是传输与重放层，不是业务事实库；MongoDB 是 Record Hub 自有事实与投影存储。

## 4. Machine identity 决策建议

| 候选 | 优点 | 缺点 | 适用结论 |
| --- | --- | --- | --- |
| Dex `client_credentials` | 与用户 issuer 统一 | 当前稳定版不支持；等待版本会阻塞交付 | 排除为近期依赖 |
| 独立 OAuth 2.0/OIDC Authorization Server | JWT contract 清晰，SDK/网关兼容好，便于 audience/scope/rotation | 多一个安全组件 | 本地及跨主机 Beta 首选 |
| SPIFFE/SPIRE 或平台 workload identity | 短期证书、强 workload attestation | 本地部署和应用接入成本高 | 生产阶段评估 |
| 自签长期 JWT/API key | 实现快 | 轮换、撤销、审计和泄露风险差 | 不作为正式方案 |

下一期先写 ADR 并以接口契约驱动实现；选型未冻结前，测试可以使用短期、测试 CA/issuer
签发的 JWT fixture，但不得把 fixture secret 带入正式部署。

## 5. 范围

### 5.1 必须交付

- machine identity ADR、独立测试 issuer/客户端、purpose-bound authorization；
- 隔离的 Mongo/NATS/Dex/Temporal/Conductor/四应用监督式验收拓扑；
- Schema compatibility diff、迁移计划和 schemaVersion 可追踪性；
- Projection source registration、mapping version、replay/rebuild operation；
- 只读 SSE 变更流，带 workspace scope、cursor、背压和断线恢复；
- 一个低风险 Command Gateway 垂直切片；
- Approver service connector 及一个 Fluxion/Bids 审批试点；
- SLO、backlog/lag 告警、备份恢复演练、审计导出和运维 runbook。

### 5.2 延期

- 任意表的自动写回、跨系统 ACID 或“通用两阶段提交”；
- 通用函数运行时、用户脚本、插件市场；
- 对象存储网关、媒体转换和签名 URL；
- Presence/Broadcast/协同光标；
- GraphQL 或任意 Mongo 查询透传；
- 多地域 active-active、自动分片和计费；
- 将 Approver 作为库嵌入其他服务，或把审批实现为跨引擎 child workflow。

## 6. 交付批次与决策门

| 批次 | 内容 | 进入下一批条件 |
| --- | --- | --- |
| P2-0 Foundations | identity ADR、隔离拓扑、补跑 MVP SKIPPED 矩阵 | 浏览器、双引擎、outage/restart、rotation 无跳过通过 |
| P2-1 Operable Data | Schema migration、source/mapping registry、rebuild、SSE | 迁移/rebuild 可回滚，跨租户与背压负向测试通过 |
| P2-2 Controlled Command | 单一低风险 command、receipt、owner Inbox、result event | ACK-loss/重复/冲突/超时/补偿证据完整 |
| P2-3 Approval Integration | Approver connector、审批试点、状态投影 | owner 边界、幂等、人工撤回/超时路径通过 |
| P2-4 Beta Readiness | SLO、容量、备份恢复、安全审查、升级说明 | Beta 验收报告签字，无未解释高风险项 |

任何批次都不得以 fake engine、内存仓库或 direct-outbox fallback 代替 live 验收。

## 7. 主要风险

- **身份范围膨胀**：通过 human/workload issuer 分离和统一 principal contract 控制。
- **Command Gateway 变成业务逻辑中心**：只负责路由、幂等、receipt 和策略；领域规则留在 owner system。
- **Schema 演进破坏长运行 workflow**：snapshot 固定 schema/record/source version，迁移不重写历史。
- **实时流泄露数据**：只发授权后的 record delta/reference，不暴露 NATS credential 或原始领域 payload。
- **审批跨引擎耦合**：采用服务 connector + stable operation ID，不共享 workflow history 或 worker 包。
