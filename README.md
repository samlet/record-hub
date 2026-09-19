# Record Hub

Record Hub 是面向 Approver、Fluxion 和 Bids 的独立多维表格与流程数据中间件，也是一套 workflow-native backend/data platform。它提供版本化 Schema、动态记录、标签与关联、跨系统状态投影、Workflow Binding 和基于 NATS JetStream 的可靠事件中继。

Record Hub 不是各业务系统的事实库，也不是跨 MongoDB、PostgreSQL、Temporal 和 Conductor 的分布式事务协调器。业务命令必须回到资源所属系统执行。

## 状态

MVP 已有条件收口：实现与本机可执行门禁完成，依赖专用 Dex/machine identity 或隔离全拓扑的 live 项已显式标为 `SKIPPED`。服务端使用 Go，Web 控制台使用 TypeScript；状态与证据见[MVP 任务分解](docs/mvp-task-breakdown.md)和[MVP 验收报告](docs/m8-acceptance-report.md)。

## 开发命令

```bash
make build
make test
make lint
make check
make generate-clients
make ci
```

服务端统一入口为 `./build/record-hub`，提供 `help`、`version` 和 `serve` 命令。`serve` 从 `RECORD_HUB_*` 环境变量加载配置，示例见 [`.env.example`](.env.example)。

HTTP 契约以 [`api/openapi.yaml`](api/openapi.yaml) 为准。`make generate-clients` 使用固定版本的 OpenAPI Generator 在 `build/generated-clients` 生成 Go server stub 以及 Go、Java 和 TypeScript 客户端。

`make ci` 在本地执行与 GitHub Actions 相同的基础门禁：格式/静态检查、测试、构建、依赖漏洞扫描、CycloneDX SBOM 生成和 Git 历史/工作树 secret 扫描。后三项需要 Docker，SBOM 输出到 `build/record-hub.cdx.json`。

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
- [M6 Workflow Binding 第一批验收](docs/m6-acceptance.md)
- [M8-080 Web/OIDC 第一批验收](docs/m8-acceptance.md)
- [M8 本地运行与排障](docs/m8-local-runbook.md)
- [M8 故障恢复与凭据轮换](docs/m8-recovery-runbook.md)
- [M8 MVP 验收报告](docs/m8-acceptance-report.md)
- [认证、授权与租户隔离](docs/security-auth.md)
- [实施路线](docs/roadmap.md)
- [下一期方案评估](docs/phase-2-evaluation.md)
- [下一期需求设计](docs/phase-2-requirements.md)
- [下一期任务分解](docs/phase-2-task-breakdown.md)
- [Phase 3 真实业务系统接入方案](docs/phase-3-integration-design.md)
- [Phase 3 真实业务系统接入需求](docs/phase-3-requirements.md)
- [Phase 3 任务分解](docs/phase-3-task-breakdown.md)
- [Phase 3 验收计划](docs/phase-3-acceptance-plan.md)
- [Phase 3 验收报告](docs/phase-3-acceptance-report.md)
- [Phase 4 Integration Beta 技术方案](docs/phase-4-design.md)
- [Phase 4 Integration Beta 需求](docs/phase-4-requirements.md)
- [Phase 4 任务分解](docs/phase-4-task-breakdown.md)
- [Phase 4 验收计划](docs/phase-4-acceptance-plan.md)
- [Phase 4 Batch 0 基线验收](docs/phase-4-batch0-baseline.md)
- [Phase 4 baseline manifest](docs/phase-4-baseline-manifest.json)
- [Phase 4 contract inventory](docs/phase-4-contract-inventory.json)
- [Phase 4 Batch 1 收口报告](docs/phase-4-batch1-closure.md)
- [Phase 4 Batch 2 P4-200 closure](docs/phase-4-batch2-p4-200.md)
- [原生隔离验收拓扑](deploy/local/isolated/README.md)
- [参考资料](docs/references.md)
- [ADR-0001：独立仓库与事实所有权](docs/adr/0001-independent-service-and-data-ownership.md)
- [ADR-0002：Go 服务端](docs/adr/0002-go-server.md)
- [ADR-0003：分离 Human 与 Workload Identity](docs/adr/0003-separate-human-and-workload-identity.md)

公共事件 envelope 契约位于 [contracts/eventenvelope/event-envelope-v1.schema.json](contracts/eventenvelope/event-envelope-v1.schema.json)。

## 仓库结构

```text
record-hub/
  server/                 Go 命令入口与模块化单体
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
