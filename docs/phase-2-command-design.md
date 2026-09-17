# P2-3 Controlled Command 设计

状态：第一段垂直切片已完成（2026-09-17）

## 当前范围

`POST /api/v1/commands` 只接受带 workload identity 的请求，并要求一个按
`tenant/workspace/policyId` 精确匹配的 command policy。policy 再绑定 owner system、resource type、action、
purpose 和是否必须携带 `expectedVersion`。Record Hub 只生成 operation receipt 和发布 command envelope，
不执行 owner system 的业务事实变更。

请求正文中的 payload 会做 JSON canonical hash；响应只返回 hash 和安全元数据，不返回 payload。相同
`Idempotency-Key` 且 hash 相同返回原 operation，hash 不同返回 `409 IDEMPOTENCY_CONFLICT`。

## 状态机与发布

新 operation 先持久化为 `ACCEPTED`，审计记录 action `command.submit`。JetStream 可用且发布成功后变为
`DISPATCHED`，发布失败仍保留 `ACCEPTED`，后续重试同一幂等键时再次尝试发布。发布消息 subject 为
`commands.<ownerSystem>.<action>.v1`，message ID 使用 operation ID。

当前批次已经冻结状态枚举 `ACCEPTED/DISPATCHED/SUCCEEDED/REJECTED/FAILED/EXPIRED`。本批增加了
`COMMAND_RESULTS` durable pull consumer：owner 事务和本地 outbox 提交后发布 `results.<ownerSystem>.<action>.v1`
结果事件，Record Hub 以 operation ID + event ID 做 CAS/幂等终态推进。Owner 侧提供 `InboxService` 的 durable
claim/commit 抽象（Mongo 与 memory 实现）；实际 owner 业务库事务仍必须由 Approver/Fluxion/Bids 自己包住，
不能把跨数据库操作误称为 exactly-once。

## 配置与安全边界

使用 `RECORD_HUB_COMMAND_POLICIES` 配置显式 JSON 数组。issuer、audience、subject、scope、tenant、workspace、
owner system、resource type、action 均必须精确匹配，禁止通配符。command policy 与 binding policy 分开配置，
避免 snapshot 权限自动升级为写权限。payload 硬上限为 256 KiB，HTTP 和 envelope 均不记录原始正文。

## 后续验收

- 增加 `APPROVAL_COMMANDS`/`OWNER_COMMANDS`/`COMMAND_RESULTS` JetStream stream 初始化和 owner durable Inbox；
- 选择一个低敏、可逆的 owner action，完成 result event、expected-version conflict 和 ACK-loss live E2E；
- 为 Go/Java/TypeScript workflow client 提供 timeout、retry、error taxonomy 和兼容矩阵；
- 补充旧 policy 撤销、owner outage、重启和 payload redaction 的隔离拓扑证据。
