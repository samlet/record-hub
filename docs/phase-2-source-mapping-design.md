# Source / Mapping Registry 设计（P2-1-003）

状态：Control plane implemented / runtime generation pending
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
  "canonicalHash": "sha256:...",
  "status": "DRAFT"
}
```

`fieldMap` 仅允许预声明的 JSON Pointer/path，最多 128 个字段；禁止表达式、脚本、Mongo
query、网络访问和原始 payload 透传。fixture 是脱敏、大小有界的 envelope+payload 样本，
用于发布前验证；生产日志不能复制 fixture 正文。

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

Projector 启动时加载指定的 published mapping generation，并缓存 `(sourceId,eventType,
eventVersion)` 的精确索引。事件没有精确 mapping、mapping 已撤销或 hash 不匹配时，必须
分类为 deterministic reject 并进入 DLQ；不能 fallback 到 source/type 的模糊匹配，也不能
直接执行未发布 draft。更新 mapping 通过新 generation + bounded drain 切换，不能覆盖正在
运行的 mapping。

现有三个 v1 summary handler 将作为内置 mapping fixture 继续运行；Registry 接入后只把它们
登记为系统拥有的 published records，不能让客户端覆盖内置 schema 或 source owner。

## 当前实现边界

本批已经实现 source/mapping 严格模型、workspace-scoped Mongo unique/index、OWNER mutation、
viewer read、optimistic revision、幂等回执、审计、canonical hash，以及 source/event version、
published target schema 和目标字段 allowlist 的发布校验。HTTP 路由和 OpenAPI 与 server runtime
已经接通。

fixture 当前只登记有界引用与 `sha256`，尚未从受控 fixture store 取回正文并执行 envelope/schema
验证；published mapping 也尚未组装为 generation 并切换现有 JetStream projector。现有硬编码 v1
summary handlers 因此仍是唯一运行时数据路径。这两项完成前 P2-1-003 保持 `PARTIAL`。

本批验收命令：

```bash
make check
RECORD_HUB_MONGODB_URI=mongodb://127.0.0.1:27017 \
  go test ./server/internal/modules/projection \
  -run TestMongoCatalogRepositoryScopesIDsAndTransitions -count=1 -v
```

## 验收门

P2-1-003 完成必须有：OpenAPI/JSON Schema、Mongo unique/index、canonical hash 稳定性、
fixture 正负样本、发布审批/撤销/幂等、跨租户拒绝和 projector 精确 lookup 的 live/恢复证据。
没有 runtime generation 与真实 JetStream 消费切换测试时只能标记 `PARTIAL`。
