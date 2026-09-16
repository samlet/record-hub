# Record Hub MVP 技术设计

- 状态：Proposed / implementation baseline
- 日期：2026-09-16
- 关联：[总体架构](architecture.md)、[任务分解](mvp-task-breakdown.md)
- 目标系统：Approver、Fluxion、Bids

## 1. MVP 目标

MVP 用一个端到端切片验证以下假设：

1. 用户能通过 Dex 登录 Record Hub，并在授权 workspace 中创建动态表格、字段、记录和 tag。
2. Approver、Fluxion、Bids 能通过各自 Transactional Outbox 向 JetStream 发布一种安全领域投影。
3. Record Hub 能至少一次消费、幂等更新投影，并明确展示版本、同步时间、积压和冲突。
4. Temporal Activity 与 Conductor Worker 都能用同一 Binding API 获取不可变 snapshot。
5. 任何一个中间件组件短暂不可用都不会使三个业务系统丢失自己的业务事实。

MVP 是 workflow data middleware，不以 Supabase feature parity 为验收目标。

## 2. 首个垂直切片

### 2.1 三个只读投影

| 来源 | 投影类型 | 最小字段 | 禁止字段 |
| --- | --- | --- | --- |
| Approver | `ApplicationSummary` | application ID、tenant、title、status、process reference、updatedAt、version | 原始表单、审批意见、用户敏感字段 |
| Fluxion | `ProjectSummary` | project ID、type、status、currentStage、workflow reference、updatedAt、version | 客户联系方式、Agent prompt/raw output |
| Bids | `TenderSummary` | tender ID、buyer organization、name、status、template/version、updatedAt、version | 未开标报价、投标内容、供应商联系人、文件 URL |

每个 source 只发布 allowlist DTO。Record Hub 不接收数据库模型或任意 JSON dump。

### 2.2 两个 Binding 验证

- Temporal：Fluxion 增加 diagnostic Activity，使用 `ProjectSummary` snapshot；Workflow history 只保存 ref/version/hash/安全摘要。
- Conductor：Bids 增加 diagnostic Worker task，使用 `TenderSummary` snapshot；task output 只保存同样的稳定引用。

这两个验证流程不修改真实业务状态，也不把 Record Hub 放到现有生产流程的关键路径。

### 2.3 多维表格体验

MVP 支持：

- workspace 和 table；
- 文本、数字、布尔、日期时间、枚举、引用六种字段；
- 创建、编辑、删除 Record Hub 自有记录；
- tag、排序、过滤、列显示/隐藏；
- 外部投影表只读；
- 记录详情展示 source reference、关系、版本和同步新鲜度；
- Schema 草稿与发布。

Schema mutation 的提交边界是 MongoDB transaction：schema definition、audit entry 和
idempotency receipt 必须一起提交；任一写入失败都回滚，客户端只有在三者同时持久化后才
收到成功响应。`Idempotency-Key` 按 tenant/workspace/operation 隔离，重复请求返回原始
definition，输入哈希变化则拒绝为冲突。

## 3. 明确不做

- 外部业务状态写回和 Command Gateway；
- 将 Fluxion/Bids 的审批迁移到 Approver；
- 公式、自动化、脚本和不受信任代码执行；
- GraphQL、SQL endpoint 或任意 Mongo query 暴露；
- 对象存储、文件上传和预签名 URL；
- Realtime Presence、协同光标或离线编辑；
- Schema.org 推理、OWL reasoner 或全局本体；
- 移动端 SDK、CLI、计费、配额和项目自助创建；
- 生产 HA、多地域、分片和灾备承诺；
- 跨系统 ACID 或 exactly-once 宣称。

## 4. 技术基线

| 层 | MVP 选择 |
| --- | --- |
| Server | Go 1.27，模块化单体 |
| API | REST/JSON，OpenAPI-first |
| Web | TypeScript + Next.js，BFF/服务端 Session |
| Record Store | MongoDB replica set |
| Messaging | NATS JetStream，file storage |
| Identity | 本地 Dex OIDC |
| Schema | JSON Schema Draft 2020-12 + 可选 semantic URI |
| Contracts | JSON Schema + fixtures + generated clients |

依赖版本在 M0 锁定并由自动化升级，不在本文中使用浮动版本。

## 5. 部署形态

```text
record-hub-server
  ├─ HTTP API
  ├─ OIDC/JWKS verifier
  ├─ projection consumers
  ├─ schema/record/view/binding modules
  └─ health/metrics

record-hub-web
  └─ Next.js BFF + grid UI

external:
  ├─ MongoDB replica set
  ├─ NATS JetStream
  └─ Dex
```

API 和 background consumers 使用同一代码库和二进制，配置允许只启动 `api`、`worker` 或 `all`。MVP 可以部署为一个进程；测试必须覆盖 API/worker 分进程重启。

## 6. 领域模型

### 6.1 Workspace 与成员

```text
Workspace
  id / tenantId / name / status / version / timestamps

WorkspaceMember
  workspaceId / principal(issuer, subject) / role
  role = OWNER | EDITOR | VIEWER
```

MVP 一个用户可以属于多个 workspace，但所有查询必须显式携带 workspace scope。

### 6.2 Schema

```text
SchemaDefinition
  id / tenantId / name / version
  status DRAFT|PUBLISHED|DEPRECATED
  jsonSchema / semanticTypes / contentHash
  createdBy / publishedBy / timestamps
```

Published schema 不可修改。表固定引用一个已发布 schema 版本；升级产生显式 migration task，MVP 不自动迁移既有记录。

### 6.3 Table、View 与 Record

```text
TableDefinition
  id / workspaceId / name / kind CUSTOM|PROJECTION
  schemaId / schemaVersion / sourcePolicy / version

ViewDefinition
  id / tableId / name / columns / filters / sorts / version

Record
  envelope + data + tags + relations
  recordVersion / createdBy / updatedBy / timestamps
```

`PROJECTION` table 和其中记录不能通过通用 Record API 修改。

### 6.4 Inbox 与投影状态

```text
InboxEvent
  eventId UNIQUE / consumer / subject / payloadHash
  status PROCESSING|APPLIED|REJECTED|FAILED
  receivedAt / appliedAt / safeError

ProjectionCheckpoint
  sourceSystem / aggregateType / aggregateId
  sourceVersion / lastEventId / syncedAt / status
```

InboxEvent、Record 更新和 AuditLog 在同一个 MongoDB transaction 中提交。

## 7. API

### 7.1 基础与身份

```http
GET  /healthz
GET  /readyz
GET  /api/v1/me
GET  /api/v1/workspaces
POST /api/v1/workspaces
```

### 7.2 Schema

```http
POST /api/v1/schemas
GET  /api/v1/schemas/{schemaId}/versions/{version}
PUT  /api/v1/schemas/{schemaId}/draft
POST /api/v1/schemas/{schemaId}/publish
```

所有写请求要求 `Idempotency-Key`。Publish 使用 `If-Match`/expected version，返回 content hash。

### 7.3 Table、View、Record

```http
POST   /api/v1/workspaces/{workspaceId}/tables
GET    /api/v1/workspaces/{workspaceId}/tables
POST   /api/v1/tables/{tableId}/views
POST   /api/v1/tables/{tableId}/indexes
GET    /api/v1/tables/{tableId}/indexes
GET    /api/v1/tables/{tableId}/records
POST   /api/v1/tables/{tableId}/records
GET    /api/v1/records/{recordId}
PATCH  /api/v1/records/{recordId}
DELETE /api/v1/records/{recordId}
```

首个 table slice 已固定资源契约：table 必须引用一个已发布的 schema 版本；`CUSTOM` 表不得
携带 `sourcePolicy`，`PROJECTION` 表必须声明 source system/type，且只保存 allowlisted
字段。所有 table 查询都显式携带 tenant/workspace scope，Mongo 以租户、workspace、table
ID 和 workspace 内名称建立唯一约束。

Record 写入要求 `Idempotency-Key`，返回 `ETag: "recordVersion"`；更新和删除必须带
`If-Match`，过期版本返回 `409 RECORD_VERSION_CONFLICT`。Record data 在写入前按 table
固定的已发布 JSON Schema 校验，projection table 的通用写操作返回
`409 PROJECTION_READ_ONLY`；该检查在 POST/PATCH/DELETE 的幂等键、版本和数据处理前执行。

Relation 只保存 typed target（`system`、`type`、`id`）和 relation type；写入时排序去重，
解析结果标记为 `CURRENT`、`BROKEN` 或 `FORBIDDEN`。`FORBIDDEN` 关系清除内部解析 ID，
防止越权侧信道。

ViewDefinition 只允许受控字段、`eq/ne/contains/in/gt/gte/lt/lte` 操作符和最多四个
排序键；schema 声明了 `properties` 时，columns/filter/sort 只能引用这些属性或有限的
envelope 字段。记录查询使用 1--100 的 bounded page size，cursor 编码排序键并自动追加
`id` 作为稳定 tie-breaker，不暴露 Mongo 查询表达式。

动态字段索引是独立的受控资源：只有 OWNER 可以创建，字段必须是 table 当前已发布 schema
中的顶层 property，每张表最多 16 个（同一字段的 asc/desc 分别计数）。服务端为索引生成
确定性的物理名称，API 不接受 Mongo key、表达式或 partial filter；索引元数据和物理 DDL
采用可重试的两步流程，DDL 失败时清理元数据。

PATCH/DELETE 必须携带 record version；冲突返回 `409 RECORD_VERSION_CONFLICT`。Projection record 的写操作返回 `409 PROJECTION_READ_ONLY`。

### 7.4 Binding

```http
POST /api/v1/bindings/snapshots
GET  /api/v1/bindings/snapshots/{snapshotId}
```

请求包含 tenant/workspace、record reference、schema version、expected record/source version 和 purpose。响应：

```json
{
  "snapshotId": "uuid",
  "recordRef": "fluxion:PROJECT:uuid",
  "schemaId": "urn:record-hub:fluxion:ProjectSummary",
  "schemaVersion": 1,
  "recordVersion": 8,
  "sourceVersion": 17,
  "snapshotHash": "sha256:...",
  "data": {}
}
```

Snapshot 创建使用稳定 operation ID；相同 key+相同 payload 返回原结果，相同 key+不同 payload 返回幂等冲突。

## 8. Canonicalization

- UTF-8 JSON；对象 key 按字典序排序。
- 数字禁止依赖二进制浮点字符串化；Schema 声明 decimal scale 时先标准化为字符串。
- 时间统一 RFC3339 UTC；日期使用 `YYYY-MM-DD`。
- tag 排序去重；关系按 `(system,type,id,relationType)` 排序。
- `snapshotHash` 覆盖 schema ID/version、record/source version 和 canonical data。
- Java、Go、TypeScript 使用共享 fixture 验证相同 hash。

## 9. JetStream 设计

MVP Stream：

```text
DOMAIN_EVENTS
  subjects = events.approver.>, events.fluxion.>, events.bids.>

DEAD_LETTERS
  subjects = dlq.record-hub.>
```

Record Hub 使用 durable pull consumer：

- Go runner 使用 server-side durable consumer，Fetch 参数（batch、timeout、重试和 drain）均有上限；
- NATS 连接启用无限重连，consumer lookup/fetch 失败只进入有界退避；handler 成功后使用
  `DoubleAck`，失败发送 `NAK`，取消时停止拉取并给当前消息一个 bounded drain 窗口。

```text
record-hub-approver-projection-v1
record-hub-fluxion-projection-v1
record-hub-bids-projection-v1
```

消费顺序：

1. 校验 subject、envelope schema、source allowlist 和大小。
2. 以 `eventId` claim Inbox。
3. 校验 payload schema、tenant mapping 和 aggregate version。
4. MongoDB transaction 更新 projection、checkpoint 和 audit。
5. transaction 成功后 ACK。
6. 确定性错误进入 REJECTED/DLQ；临时错误 NAK/退避重投。

Handler registry 按 `sourceSystem/eventType/schemaVersion` 精确匹配，注册键全局唯一；不允许
按 source 或 event type 回退匹配，未注册事件直接 fail closed，避免未知事件误写投影。

MVP 单消息上限暂定 256 KiB；压测后才能提高。

## 10. 三个生产者接入

### 10.1 Approver

新增 `ApplicationSummaryChanged@v1` Outbox 事件。只在 Application 安全摘要或状态版本前进时发布。不得从 `integration_result_outbox` 推导通用 Application 事件。

### 10.2 Fluxion

新增通用 domain outbox 最小表/dispatcher，在 Project 状态或 current stage 事务中写 `ProjectSummaryChanged@v1`。不能从 Temporal visibility 轮询构造事实。

### 10.3 Bids

扩展现有 `outbox_events` 和 dispatcher，发布 `TenderSummaryChanged@v1`。必须与现有 Conductor command/finance command 分类隔离，不能让 Record Hub consumer 领取内部命令。

三个生产者均使用 event ID 作为 NATS message ID，并在 NATS 确认 publish 后标记 Outbox sent。响应丢失时用原 event ID 重发。

## 11. 身份与授权

### 11.1 用户

- Record Hub Web 使用独立 Dex client 和 Authorization Code + PKCE。
- BFF 建立 HttpOnly、Secure、SameSite Session。
- 后端以 `(iss, sub)` 查本地 membership。
- OWNER 管理 workspace/schema；EDITOR 修改 custom records/views；VIEWER 只读。

### 11.2 服务

- 三个 producer 使用独立 NATS principal 和 subject publish 权限。
- Fluxion/Bids diagnostic adapter 使用各自 Dex machine client 调 Binding API。
- Machine client 映射到明确 tenant/workspace/purpose allowlist。
- 用户 token、Dex client secret、NATS credential、Mongo credential 不复用。

## 12. Web MVP

页面：

```text
/login
/workspaces
/workspaces/{id}/tables
/tables/{id}
/schemas
/schemas/{id}
/operations/events
```

表格页支持分页、排序、过滤、tag、列显隐和记录详情。Projection table 显示只读徽标、source system、source version、syncedAt、lag/gap 状态。

Operations 页只展示安全错误、consumer、event ID、aggregate reference、delivery count 和状态，不展示 token、原始敏感 payload 或连接凭据。

MVP 更新采用普通 HTTP 刷新或轻量 SSE；不实现 Presence/Broadcast。

## 13. 可靠性与失败矩阵

| 故障 | 预期行为 |
| --- | --- |
| NATS 不可用 | 业务系统 Outbox 积压，业务事务继续 |
| Record Hub worker 停止 | Durable consumer 积压，恢复后继续 |
| MongoDB 不可用 | 不 ACK，消息退避重投 |
| Mongo commit 后 ACK 丢失 | Inbox 命中，重复消息安全 ACK |
| 事件重复 | eventId/payloadHash 相同视为成功 |
| eventId 相同 payload 不同 | 安全拒绝并告警 |
| aggregate version 落后 | 忽略并记录 duplicate |
| aggregate version 跳跃 | projection 标记 GAP，停止覆盖该 aggregate |
| Dex/JWKS 暂时不可用 | 已缓存有效 JWKS 可验证；过期后 fail closed |
| Record Hub 不可用 | 现有业务 workflow 不受影响；diagnostic workflow 可重试/失败 |

## 14. 可观测性

至少提供：

- HTTP request count/latency/error；
- consumer pending、redelivery、NAK、DLQ；
- projection applied/duplicate/gap/rejected；
- oldest event age 和 source-to-projection lag；
- Mongo transaction retry/conflict；
- OIDC verification failure（低基数原因）；
- structured log correlation：requestId/eventId/tenantId/aggregate reference。

禁止把 token、secret、完整 payload、投标内容和个人敏感数据写入日志或 metric label。

## 15. 验收场景

1. 用户经 Dex 登录，创建 workspace、发布 schema、创建表和记录，并通过 tag 过滤。
2. 未授权用户不能读取其他 workspace；EDITOR 不能发布 schema；VIEWER 不能写记录。
3. 三个系统各产生一个真实业务状态事件，Record Hub 出现三个只读投影。
4. 重复发布同一事件不产生第二条记录或第二个版本。
5. 人为制造版本 gap，投影显示 GAP 且不覆盖现有状态；补齐后可恢复。
6. 停止 Record Hub worker 后产生事件，重启后积压清空。
7. Mongo commit 后模拟 ACK 丢失，重投不会产生重复副作用。
8. Fluxion diagnostic Workflow 通过 Activity 固定 snapshot，并能 replay。
9. Bids diagnostic Workflow 通过 Worker 固定同一 snapshot 契约。
10. Bids 投影和事件中不包含禁止字段。
11. 过期/错误 audience token、跨 tenant machine client 和越权 NATS subject 均被拒绝。
12. 所有契约 fixture、单元测试、集成测试和端到端脚本可重复执行。

## 16. MVP 完成定义

- [任务分解](mvp-task-breakdown.md) 中 M0-M8 全部 DONE，M9 的生产 HA 项可保持 DEFERRED。
- 三个真实 source projection 和两个 engine binding 验收通过。
- 无跨系统写回，无业务系统通过 Record Hub 修改最终事实。
- 安全负向测试、故障注入、重启恢复和契约兼容测试通过。
- 提供本地启动、升级、备份、DLQ/gap 恢复和 credential rotation runbook。
