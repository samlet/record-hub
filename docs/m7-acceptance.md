# M7-070 权限边界负向矩阵验收

M7-070 已完成。Record Hub 的 workflow binding、projection summary 和记录存储边界均有
可重复的负向测试，拒绝结果不把敏感字段回显给调用方。

## 覆盖矩阵

| 边界 | 负向案例 | 预期 |
| --- | --- | --- |
| 用户 → binding create | 跨 tenant、跨 workspace | `identity.ErrForbidden` |
| 用户 → binding get | 已存在 snapshot 的跨 tenant、跨 workspace | `identity.ErrForbidden`，不暴露 snapshot |
| machine → binding create | 跨 tenant、跨 workspace、purpose、resource type、audience、identity | `ErrMachinePolicyDenied` |
| binding row | 请求 `recordRef` 与 adapter 返回的稳定行引用不一致 | `ErrRecordNotFound` |
| summary fields | Approver、Fluxion、Bids 的 contact/phone/报价/quotation/file URL 等字段 | deterministic reject，错误不包含字段值 |

实现测试：

- `server/internal/modules/binding/m7_authorization_matrix_test.go`
- `server/internal/modules/projection/summary_test.go` 中的 `TestM7SummaryFieldBoundaryRejectsSensitiveFieldsForEveryProducer`

## 运行验收

```bash
go test ./server/internal/modules/identity ./server/internal/modules/binding ./server/internal/modules/projection
go test ./...
```

M7-071 的跨仓库安全 payload gate：

```bash
./scripts/verify-m7-payload-scan.sh
```

该 gate 固化四个仓库的 summary fixture、Outbox payload、workflow/task 输出和 Record Hub
operations/API 脱敏测试；它只使用 deterministic fake，不把未启动的 MongoDB、NATS、Temporal、
Conductor 或 Dex 伪报成 live E2E。

## M7-072 metrics 与 structured logging

Record Hub API 暴露 `/metrics`，使用无外部依赖的 Prometheus text format registry；HTTP middleware
记录请求总数、状态码、认证失败和延迟，OperationsService 记录 projection backlog/failure
gauge，PullRunner 记录 redelivery、version gap 和 DLQ 成功投递。所有 label 只使用 method、
status、consumer 等有界维度，不使用 tenant/record/event/token/payload。

结构化日志仍由 `server/internal/logging` 统一输出 JSON；业务错误保持安全摘要，原始 payload
与凭据不进入日志。metrics 与 middleware 的并发、顺序、路径脱敏测试位于
`server/internal/observability/metrics_test.go`。

```bash
go test ./server/internal/observability ./server/internal/app ./server/internal/modules/projection
```

## 边界说明

binding 只接受 `system:type:id` 的稳定引用和已经由 projection/record owner 过滤后的 JSON
对象；它不会把任意数据库查询或完整业务模型暴露给 workflow。字段级 allowlist 由三个
summary schema 的 `additionalProperties: false` 合同和 handler 执行，新增字段必须先发布
新的契约版本。Mongo record/source 查询始终同时带 tenant、workspace 和 source identity
条件。
