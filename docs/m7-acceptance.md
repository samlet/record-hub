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

## M7-073 Mongo commit/ACK loss

Projection repository 提供一个仅用于故障验收的 post-commit hook；live 测试先提交
Inbox/record/checkpoint/audit，再模拟客户端响应丢失，随后用同一 event ID 重投。重投通过
Inbox 的 `(consumer,eventId)` 去重，四类文档数量和 recordVersion 必须保持 1/原值。

```bash
./scripts/verify-m7-mongo-faults.sh
```

当前环境未提供 `RECORD_HUB_MONGODB_URI`，因此该项保持 `PARTIAL`；脚本默认只报告跳过，不
伪报真实 Mongo 故障注入通过。启动本地 replica set 后设置
`RECORD_HUB_M7_MONGO_LIVE=1` 执行 live gate。

## M7-074 NATS outage/outbox recovery

四个仓库的确定性 gate 已覆盖：Record Hub durable pull 在 Fetch/handler 错误后重新取得
consumer，失败消息 NAK 或进入 DLQ；Approver、Fluxion、Bids 的 summary relay 在 publish
ACK loss 后保留同一 outbox/event ID，等待 lease/retry。业务事务与 relay 解耦，故障期间不会
要求业务事务同步依赖 NATS。

```bash
./scripts/verify-m7-nats-recovery.sh
```

该脚本不会停止当前本地进程。NATS 宕机、恢复和 backlog 清空的真实场景需要在本地 Mongo/
PostgreSQL/NATS 全部启动后执行；设置 `RECORD_HUB_M7_NATS_LIVE=1` 会追加 NATS connectivity
smoke，但当前环境尚未完成真实 outage/recovery，因此 M7-074 保持 `PARTIAL`。

## M7-075 Dex/JWKS rotation/outage

OIDC verifier 的自动化验收覆盖：已缓存旧 key 在轮换后仍可验证、新 `kid` 触发 JWKS 刷新、
JWKS outage 下已缓存 key 可继续使用、未知 key 在 outage 下 fail closed，以及错误 issuer、
audience 和过期 token 均拒绝。测试 issuer 不依赖真实 Dex，避免把本地服务状态误当成缓存边界
证据；旧 key 缓存回归测试位于 `server/internal/modules/identity/verifier_test.go`。

```bash
./scripts/verify-m7-dex-rotation.sh
```

设置 `RECORD_HUB_M7_DEX_LIVE=1` 会在上述 verifier 测试后追加本地 Dex discovery/PKCE smoke；
当前 M7-075 的核心 rotation/outage 验收已由 deterministic verifier 测试完成。

## M7-076 API/worker/Mongo/NATS restart

`scripts/verify-m7-restart.sh` 构建当前二进制，分别启动/停止两次 API 与 worker，验证
`SIGTERM` 下 API 健康探针可恢复、worker 可有界退出。Projection runner 的 durable consumer
重连和 in-flight drain 由 M7-074 gate 覆盖。

```bash
./scripts/verify-m7-restart.sh
```

Mongo/NATS 的真实进程重启还需要本地 replica set、JetStream 和业务 outbox 同时运行，并应
在受控环境记录 backlog、lease 和 event ID；当前只完成 API/worker process smoke，因此
M7-076 保持 `PARTIAL`，不把 idle worker 当成完整业务 worker 验收。

## M7-077 bounded query/payload/rate limit

现有记录、view、operations、Inbox 和 summary envelope 均有上限；binding 请求体限制为
2 MiB，projection envelope 限制为 256 KiB，列表/operations/index 查询限制为 1–100 或
每表 16 个 index。API 进程额外启用 process-wide fixed-window limiter（120 requests/min），
不按 IP、tenant 或 token 建立无界状态；超限返回安全的 `429 RATE_LIMITED` 和 `Retry-After`。

```bash
./scripts/verify-m7-bounds.sh
```

## 边界说明

binding 只接受 `system:type:id` 的稳定引用和已经由 projection/record owner 过滤后的 JSON
对象；它不会把任意数据库查询或完整业务模型暴露给 workflow。字段级 allowlist 由三个
summary schema 的 `additionalProperties: false` 合同和 handler 执行，新增字段必须先发布
新的契约版本。Mongo record/source 查询始终同时带 tenant、workspace 和 source identity
条件。
