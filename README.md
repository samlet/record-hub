# Record Hub

Record Hub 是面向 Approver、Fluxion 和 Bids 的独立多维表格与流程数据中间件，也是一套 workflow-native backend/data platform。它提供版本化 Schema、动态记录、标签与关联、跨系统状态投影、Workflow Binding 和基于 NATS JetStream 的可靠事件中继。

Record Hub 不是各业务系统的事实库，也不是跨 MongoDB、PostgreSQL、Temporal 和 Conductor 的分布式事务协调器。业务命令必须回到资源所属系统执行。

## 状态

当前处于架构设计阶段。服务端确定使用 Go，Web 控制台使用 TypeScript；尚未开始生产代码。

## 核心边界

- Approver、Fluxion、Bids 分别拥有自己的业务事实和领域事件。
- MongoDB 保存 Record Hub 自有记录、协作字段、Schema 和物化投影。
- NATS JetStream 保存可重放的事件和命令消息；各生产者仍保留本地 Transactional Outbox。
- Workflow 节点通过稳定引用、Schema 版本、记录版本和 snapshot hash 使用 Record Hub。
- Dex 为 Web 用户提供统一 OIDC 登录，并可在本地环境为服务间调用签发 client-credentials token。
- NATS 和 MongoDB 使用各自的机器身份，不复用 Dex 用户 token。

## 文档

- [总体架构](docs/architecture.md)
- [产品定位与 Supabase 对比](docs/product-positioning.md)
- [MVP 技术设计](docs/mvp-design.md)
- [MVP 任务分解](docs/mvp-task-breakdown.md)
- [Schema 与记录模型](docs/schema-model.md)
- [事件与 JetStream 契约](docs/event-contract.md)
- [Workflow Binding 与事务语义](docs/workflow-binding.md)
- [认证、授权与租户隔离](docs/security-auth.md)
- [实施路线](docs/roadmap.md)
- [参考资料](docs/references.md)
- [ADR-0001：独立仓库与事实所有权](docs/adr/0001-independent-service-and-data-ownership.md)
- [ADR-0002：Go 服务端](docs/adr/0002-go-server.md)

公共事件 envelope 的初始草案位于 [contracts/event-envelope-v1.schema.json](contracts/event-envelope-v1.schema.json)。

## 计划中的仓库结构

```text
record-hub/
  server/                 Go API、Schema、Record、Projection、Binding
  web/                    多维表格 UI
  contracts/              Record Hub 自有公共契约
  sdk/
    java/                 Approver/其他 JVM 客户端
    go/                   Bids 客户端
    typescript/           Web/BFF 客户端
  deploy/local/           本地 Dex、NATS、MongoDB 开发说明
  docs/
```

首期保持单仓库、单后端部署单元；只有在容量、权限或发布节奏出现明确分化后才拆服务。

Record Hub 的目标是在动态数据、认证、实时能力和 SDK 体验上逐步接近 Backend-as-a-Service，但不会复制 Supabase 的 PostgreSQL 产品。差异和缺口见[产品定位](docs/product-positioning.md)。
