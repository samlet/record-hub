# ADR-0002：Go 服务端

- 状态：Accepted
- 日期：2026-09-16

## 背景

Record Hub 需要同时承载 REST API、MongoDB 事务、NATS JetStream durable consumer、投影 worker、WebSocket/SSE Gateway 和多租户策略。调用方包含 Java/Kotlin 和 Go 系统，因此不能依赖服务端语言共享业务实现。

## 决策

1. 服务端使用 Go，初始工具链基线为本机已安装的 Go 1.27。
2. Web 控制台使用 TypeScript，与服务端通过公共 API 交互。
3. Go module path 固定为 `github.com/samlet/record-hub`。
4. API 采用 OpenAPI-first，生成 Java、Go 和 TypeScript client；不得让调用方导入服务端内部包。
5. 首期构建模块化单体，并在同一二进制中运行 API 和可独立开关的 background workers。
6. MongoDB、NATS、Dex 都通过窄接口封装，领域层不直接依赖驱动类型。

## 建议目录

```text
server/
  cmd/record-hub/          API 与 worker 入口
  internal/config/
  internal/auth/
  internal/schema/
  internal/record/
  internal/view/
  internal/projection/
  internal/binding/
  internal/command/
  internal/eventing/
  internal/audit/
  internal/platform/
    mongodb/
    nats/
    dex/
  api/openapi/
```

## 理由

- Go 适合长期运行的 API、consumer 和网络服务，部署为单一静态二进制简单。
- NATS 与 MongoDB 都有成熟的官方 Go client。
- Bids 团队已有 Go 经验，但 Bids 与 Record Hub 仍只共享公共契约，不共享内部源码。
- Java/Kotlin 系统通过生成 client 和事件 schema 集成，避免语言与框架耦合。

## 约束

- 动态 Schema 不能退化为到处使用 `map[string]any`；边界解析后应转换为有类型的 envelope/value objects。
- JSON Schema validator、canonical JSON/hash 和 decimal/time 语义必须有跨语言 fixture。
- WebSocket 高连接数、表达式/公式执行和用户脚本 sandbox 必须单独压测；必要时可拆专用运行时，但不提前拆分。
- Go 服务端不直接执行不受信任的 JavaScript/公式代码。
