# Record Hub Server

服务端确定使用 Go，详细决策见 [ADR-0002](../docs/adr/0002-go-server.md)。

Go module path 已固定为 `github.com/samlet/record-hub`。生产代码将在首期 API 和依赖版本冻结后初始化。计划中的模块包括 Schema、Record、View、Projection、Workflow Binding、Command Gateway、Eventing、Auth 和 Audit。
