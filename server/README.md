# Record Hub Server

服务端确定使用 Go，详细决策见 [ADR-0002](../docs/adr/0002-go-server.md)。

Go module path 已固定为 `github.com/samlet/record-hub`。统一命令入口位于 `cmd/record-hub`，业务能力按模块化单体组织在 `internal/modules` 下；基础设施适配器将在对应 MVP 任务中加入。

从仓库根目录运行 `make check` 执行格式检查、静态分析、测试和构建。
