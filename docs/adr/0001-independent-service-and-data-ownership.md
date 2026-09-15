# ADR-0001：独立服务与事实所有权

- 状态：Accepted
- 日期：2026-09-16

## 背景

Approver、Fluxion 和 Bids 需要共享动态表格、Schema、状态速查和 Workflow Binding，但使用不同领域模型与 Workflow Engine，且必须独立发布。

## 决策

1. Record Hub 位于独立仓库 `/Users/xiaofeiwu/apps/record-hub`。
2. 首期使用单个后端部署单元、独立 MongoDB 数据库和 NATS JetStream。
3. 各业务系统继续拥有最终业务事实，并通过本地 Outbox 发布领域事件。
4. Record Hub 的外部资源记录默认为只读投影。
5. 外部业务修改通过受控 command 返回 owner system。
6. Workflow Binding 提供 snapshot、CAS、幂等 receipt 和 Saga，不提供跨系统 ACID。
7. 本地统一使用 Dex OIDC；用户身份、机器身份、NATS 身份和 MongoDB 身份相互分离。

## 结果

优点：

- 不绑定某个业务系统或 Workflow Engine；
- 可以独立扩缩容、发布和授权；
- 支持统一 UI 和检索，同时保护事实边界；
- JetStream 消费者可独立重放和演进。

代价：

- 引入新的服务、MongoDB、NATS 和 Dex 运维面；
- 所有投影均为最终一致；
- 需要维护 Schema、事件兼容性和跨系统对账；
- 无法通过共享数据库获得虚假的强一致性。

