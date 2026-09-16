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

`semanticTypes` 在写入前排序去重：Schema.org 的 `http`/`www` 形式统一为 `https://schema.org/...`，自有 Web 标识只接受 HTTPS，也可使用 `urn:`。带 userinfo、query、fragment、相对路径或缺失类型路径的 URI 被拒绝。规范化结果用于查询和 hash，但只是 annotation，不能改变结构校验结果。

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
- Compatibility checker 按“旧版本接受的实例仍应被新版本接受”判断：移除 required、`integer` 放宽为 `number`、扩展 enum 属于兼容；新增 required、删除属性、收窄 type/enum 或改变其他约束属于 breaking。MVP 对无法证明安全的约束变化保守判 breaking。
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

## 6. Canonical JSON 与 Schema content hash

Canonical JSON 使用以下跨语言规则：

- 输入必须是单一、合法 UTF-8 JSON，重复对象 key 和尾随值直接拒绝；
- 对象 key 按 UTF-8 byte order 排序，array 顺序不变；
- string 保留 UTF-8，只对 JSON 必需字符和 U+0000—U+001F 转义；
- number 以任意精度十进制解析，输出无 exponent、无无意义前导/尾随零的 plain decimal，`-0` 统一为 `0`；
- decimal scale 具有业务含义时，值必须先按 schema 规范化为定长字符串，不能经过 binary float；
- Schema content hash 对 `{jsonSchema, semanticTypes}` 的 canonical bytes 计算 SHA-256，并使用 `sha256:<lowercase hex>` 表示。

跨语言 fixture 位于 `server/internal/modules/schema/testdata/schema-content-hash.json`；Java、Go、TypeScript 实现必须产生其中同一个 `expectedHash`。
