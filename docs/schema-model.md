# Schema 与记录模型

## 1. 双层 Schema

Record Hub 不以 Schema.org 代替严格校验，而采用双层模型：

1. JSON Schema Draft 2020-12：结构、必填项、枚举、格式和兼容性。
2. Schema.org/自有 URI：跨系统语义、类型归类和属性提示。

示例：

```json
{
  "schemaId": "urn:record-hub:bids:TenderApproval",
  "version": 3,
  "semanticTypes": [
    "https://schema.org/Action",
    "https://schema.org/ReviewAction"
  ],
  "validationSchema": "urn:record-hub:schema:bids-tender-approval:3"
}
```

无法准确拟合 Schema.org 的领域概念必须保留自有 URI，不创建错误的公共语义映射。

MVP validator 强制声明 Draft 2020-12，并在编译时校验 schema vocabulary 和 format。实例解析拒绝非法 UTF-8、多 JSON 值及尾随内容；文本、数字、布尔、`date-time`、枚举和 typed reference 六种字段均通过相同编译器验证。数字规范化与跨语言 hash 属于 canonicalization 层，不由 JSON Schema validator 猜测 decimal scale。

## 2. Schema 生命周期

```text
DRAFT -> PUBLISHED -> DEPRECATED
```

- Published 版本不可原地修改。
- MongoDB 同时以 `(tenantId, name, version)` 和 `(tenantId, schemaId, version)` 建立唯一索引；草稿更新使用 `revision` CAS。
- 通用更新只匹配 `DRAFT` 状态。`PUBLISHED`/`DEPRECATED` 即使并发调用或伪装成 draft 也不能通过 repository 改写。
- 增加可选字段可以发布兼容小版本。
- 删除、改名、改变类型/必填语义或枚举含义必须发布新主版本。
- 记录固定 `schemaId + schemaVersion`，不得自动漂移到最新版。
- Schema 迁移生成新记录版本并保留迁移来源。

## 3. Record envelope

```json
{
  "id": "uuid",
  "tenantId": "uuid",
  "workspaceId": "uuid",
  "tableId": "uuid",
  "schemaId": "urn:record-hub:bids:Tender",
  "schemaVersion": 1,
  "source": {
    "system": "bids",
    "type": "TENDER",
    "id": "uuid",
    "version": 17
  },
  "recordVersion": 8,
  "tags": ["high-value", "awaiting-approval"],
  "data": {},
  "projection": {
    "lastEventId": "uuid",
    "syncedAt": "2026-09-16T10:00:00Z",
    "status": "CURRENT"
  }
}
```

Envelope 字段由服务控制。用户只能编辑 Schema 和权限允许的 `data` 字段以及授权 tag。

## 4. 引用与关系

关系保存稳定引用，不复制对方完整内容：

```json
{
  "target": {
    "system": "approver",
    "type": "APPLICATION",
    "id": "uuid"
  },
  "relationType": "approval-for",
  "resolvedRecordId": "uuid"
}
```

跨系统引用不声明数据库外键或级联删除。目标消失、不可访问或版本不兼容时，引用进入 `BROKEN`/`FORBIDDEN` 状态。

## 5. 索引基线

至少建立：

- `(tenantId, workspaceId, tableId, id)`；
- `(tenantId, source.system, source.type, source.id)` 唯一索引；
- `(tenantId, schemaId, schemaVersion)`；
- 记录版本 CAS 条件；
- 常用 tag、投影状态和同步时间索引。

任意动态字段索引必须由受控的 View/Index Policy 创建，不能由用户无限制创建索引。
