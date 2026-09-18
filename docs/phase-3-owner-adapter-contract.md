# Phase 3 Owner Adapter Contract

本说明是 Approver、Fluxion、Bids 实现 owner adapter 的最小公共边界。实现方只需要复制
`contracts/commands/` 下的 schema、fixture、错误码和 manifest；不得依赖 Record Hub 的
`server/internal` 包、Mongo model 或内部 repository。

## Wire contract

- command subject：`commands.<ownerSystem>.<action>.v1`
- result subject：`results.<ownerSystem>.<action>.v1`
- payload 是 JSON object，不是 base64 字符串；`payloadHash` 是 canonical object bytes 的
  `sha256:<64 lowercase hex>`。
- command/result 最大 256 KiB，UTF-8、单一 JSON value、禁止未知字段和尾随 JSON。
- command 的 `operationId` 是幂等键；result 的 `eventId` 是幂等键。所有 tenant/workspace、owner、
  action 和 operation 关联字段必须原样回传。
- result 终态仅允许 `SUCCEEDED`、`REJECTED`、`FAILED`、`EXPIRED`；对外只返回 manifest 中的
  `errorCode` 和不超过 512 字符的 `safeError`。

## Owner transaction

owner consumer 在 ACK command 前必须完成同一 PostgreSQL transaction：

1. 以 `(tenantId, workspaceId, operationId)` 唯一插入或锁定 Inbox，并比较 `payloadHash`。
2. 校验服务端 tenant/workspace mapping、resource ref、expected version 和 action allowlist。
3. 写入领域事实、审计、Inbox 终态和 result Outbox；任何一步失败都回滚。
4. transaction commit 后才 ACK；独立 relay 发送 result，使用 `eventId` 作为 JetStream message ID。
5. 相同 operation/hash 重投只返回原 result；相同 operation/不同 hash 返回
   `IDEMPOTENCY_CONFLICT`，不得再次执行领域副作用。

result Outbox 不得保存 token、原始敏感正文、SQL、堆栈或文件 URL。可重试的数据库/NATS 故障不应
提前生成 FAILED 终态；达到重试上限后进入有界 DLQ 并保留可审计的安全错误码。

## First slices

- Fluxion first command：`project.annotate` 的 `APPEND`/`VOID`，不得改变 workflow stage、派单、
  收款或补偿状态。
- Approver 与 Bids 的 owner adapter 在后续批次按同一 Inbox/Outbox 语义接入；本批只冻结公共契约，
  不宣称真实业务已经连通。

跨仓库契约门禁：在 record-hub 根目录运行 `make p3-contract-gate`。路径可用
`APPROVER_ROOT`、`FLUXION_ROOT`、`BIDS_ROOT` 覆盖；门禁通过后才可进入 owner adapter 实现。
