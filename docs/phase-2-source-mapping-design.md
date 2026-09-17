# Source / Mapping Registry 设计（P2-1-003）

状态：Implemented / accepted
日期：2026-09-17

## 目标

把 producer 的事件契约和 Record Hub 的 projection mapping 从代码中的隐式约定提升为可查询、
可审核、可回滚的控制面资源。Registry 只管理“允许什么、映射到哪里、使用哪个版本”，不接管
Approver、Fluxion 或 Bids 的业务事实，也不执行任意用户脚本。

## SourceRegistration

一个 source registration 标识一个受信 producer 与事件族：

```json
{
  "sourceId": "fluxion",
  "tenantId": "tenant-1",
  "workspaceId": "workspace-1",
  "eventType": "fluxion.project.summary-changed",
  "eventVersion": 1,
  "ownerContact": "fluxion-platform@example.test",
  "tenantResolution": {"mode":"metadata", "field":"workspaceId"},
  "status": "DRAFT"
}
```

约束：`sourceId`、`eventType`、`eventVersion` 精确唯一；`ownerContact` 只作为运维联系信息，
不参与授权；tenant/workspace resolution 只能选择 `metadata` 或显式 allowlist，禁止把任意
payload 字段解释为租户边界。注册、发布和撤销都保留 `(iss, sub)` actor 与 revision。

## MappingRegistration

Mapping 绑定 source event 到一个 projection table/schema：

```json
{
  "mappingId": "fluxion-project-summary-v1",
  "sourceId": "fluxion",
  "eventType": "fluxion.project.summary-changed",
  "eventVersion": 1,
  "targetTableId": "projection-fluxion-summary",
  "targetSchemaId": "urn:record-hub:summary:project:v1",
  "targetSchemaVersion": 1,
  "fieldMap": {"name":"payload.name", "status":"payload.status"},
  "fixture": {"eventRef":"fixtures/fluxion/project-summary-v1.json", "sha256":"sha256:..."},
  "fixtureDocument": {"eventId":"...", "kind":"event", "payload":{"name":"..."}},
  "canonicalHash": "sha256:...",
  "status": "DRAFT"
}
```

`fieldMap` 仅允许预声明的 JSON Pointer/path，最多 128 个字段；禁止表达式、脚本、Mongo
query、网络访问和原始 payload 透传。创建 mapping 时必须提交脱敏、大小有界的
`fixtureDocument`；服务将其 canonicalize 后校验 `fixture.sha256`，存入独立 collection，响应和
生产日志都不能复制正文。

## 生命周期与 API

```text
POST /api/v1/sources
GET  /api/v1/sources?tenantId=&workspaceId=
POST /api/v1/sources/{sourceId}/publish
POST /api/v1/mappings
GET  /api/v1/mappings?tenantId=&workspaceId=&sourceId=
POST /api/v1/mappings/{mappingId}/publish
POST /api/v1/mappings/{mappingId}/revoke
```

所有 mutation 都要求 OWNER/受权 operator、`Idempotency-Key` 和 `X-Request-ID`，使用 Mongo
transaction 同时写 registry、receipt 和 audit。发布前服务端必须：

1. 校验 source registration 已发布且 event version 精确匹配；
2. 校验 target schema 已发布并且 mapping 的字段均在 allowlist 内；
3. canonicalize JSON 后计算 `canonicalHash`，用 hash 固化发布内容；
4. 运行 fixture，通过 envelope、schema、workspace resolution 和 field allowlist 检查；
5. 仅发布一个不可变 revision；撤销只改变指针状态，不删除历史版本。

## Runtime 读取规则

Projector 启动时从 Mongo 组装 published mapping generation，并缓存 `(tenantId,workspaceId,
sourceId,eventType,eventVersion)` 的精确索引。事件没有精确 mapping、mapping 已撤销或 hash 不匹配时，必须
分类为 deterministic reject 并进入 DLQ；不能 fallback 到 source/type 的模糊匹配，也不能
直接执行未发布 draft。更新 mapping 通过新 generation + bounded drain 切换，不能覆盖正在
运行的 mapping。

现有三个 v1 summary handler 将作为内置 mapping fixture 继续运行；Registry 接入后只把它们
登记为系统拥有的 published records，不能让客户端覆盖内置 schema 或 source owner。

## Runtime generation 实现

当前已经实现 source/mapping 严格模型、workspace-scoped Mongo unique/index、OWNER mutation、
viewer read、optimistic revision、幂等回执、审计、canonical hash，以及 source/event version、
published target schema 和目标字段 allowlist 的发布校验。HTTP 路由和 OpenAPI 与 server runtime
已经接通。

fixture 正文已采用独立 Mongo collection 受控存储，mapping 只公开 `eventRef` 和 canonical JSON
`sha256`。发布时会重新校验 hash、Event Envelope v1、source/type/version、tenant/workspace
resolution、JSON Pointer/path 存在性，以及映射结果的目标 JSON Schema；fixture 正文不会进入
mapping/list 响应、receipt 或 audit。

worker 启动时会读取全部 published mappings，重新校验 registration/canonical hash、source 状态、
target schema 状态与字段 allowlist，并预编译目标 JSON Schema。generation ID 由排序后的 mapping、
source revision 和 schema content hash 计算，不依赖加载时间；完整 generation 构建成功后才通过
atomic pointer 一次切换。后台每 5 秒刷新，Mongo 暂时不可用或任一 entry 无效时保留
last-known-good generation，不暴露半组装状态。

动态 projector 先按 tenant/source/type/version 精确定位候选，再执行 source 声明的 metadata 或
allowlist workspace resolution，最终命中完整精确键。真实事件重新经过 Event Envelope v1、受限
fieldMap 和编译后的 target schema 校验。记录版本使用 aggregateVersion，Inbox/checkpoint/audit 与
现有 durable consumer 事务路径保持一致。撤销 mapping 后，新 generation 不再包含该 key，后续事件
成为 deterministic reject。

三个内置 summary handler 继续作为显式 built-in 路径；其三个完整 key 被保留，catalog generation
拒绝覆盖。其他 Approver、Fluxion、Bids 事件可以走动态 mapping；没有对应 JetStream durable 的
source 会在 generation 构建阶段失败，而不是成为永远不可消费的配置。

本批验收命令：

```bash
make check
RECORD_HUB_MONGODB_URI=mongodb://127.0.0.1:27017 \
  go test ./server/internal/modules/projection \
  -run TestMongoCatalogRepositoryScopesIDsAndTransitions -count=1 -v
make p2-mapping-recovery
```

`p2-mapping-recovery` 使用真实 Mongo replica set 和 JetStream：先在线消费 version 1，停止 worker，
在停机期间把 version 2 写入同一 durable consumer，再从 Mongo 重新组装 generation 并恢复消费；
最后断言 projection、checkpoint 和两个 `APPLIED` Inbox 记录。

## 验收门

P2-1-003 完成门已经满足：OpenAPI/JSON Schema、Mongo unique/index、canonical hash 稳定性、
fixture 正负样本、发布审批/撤销/幂等、跨租户拒绝、projector 精确 lookup，以及真实 Mongo +
JetStream durable 的 generation 重建/恢复证据均已通过。
