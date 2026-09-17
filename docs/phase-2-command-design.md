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

当前批次已经冻结状态枚举 `ACCEPTED/DISPATCHED/SUCCEEDED/REJECTED/FAILED/EXPIRED`，但 owner Inbox、结果
事件和终态推进留给下一批；因此不宣称 exactly-once 或业务执行成功。

## 配置与安全边界

使用 `RECORD_HUB_COMMAND_POLICIES` 配置显式 JSON 数组。issuer、audience、subject、scope、tenant、workspace、
owner system、resource type、action 均必须精确匹配，禁止通配符。command policy 与 binding policy 分开配置，
避免 snapshot 权限自动升级为写权限。payload 硬上限为 256 KiB，HTTP 和 envelope 均不记录原始正文。

## 后续验收

- 增加 `APPROVAL_COMMANDS`/`OWNER_COMMANDS` JetStream stream 初始化和 owner durable Inbox；
- 选择一个低敏、可逆的 owner action，完成 result event、expected-version conflict 和 ACK-loss live E2E；
- 为 Go/Java/TypeScript workflow client 提供 timeout、retry、error taxonomy 和兼容矩阵；
- 补充旧 policy 撤销、owner outage、重启和 payload redaction 的隔离拓扑证据。
