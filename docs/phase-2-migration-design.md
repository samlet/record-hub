# Schema Migration Plan 设计（P2-1-002）

状态：Registration API implemented; execution pending
日期：2026-09-17

## 目标与边界

Migration Plan 是 Schema 版本之间的可审计执行意图，不是跨数据库事务，也不修改已有
snapshot/history。每个计划固定引用一个 `fromVersion` 和 `toVersion`，执行器只能针对该
版本对记录做逐条 CAS/operation-id 迁移；中断后可以从 checkpoint 继续，重复投递不得重复
副作用。

第一阶段只实现计划登记、兼容性快照和取消；实际记录转换器与 staging generation 在
P2-1-004 Projection rebuild 之后接入。这样可以先冻结控制面契约，再避免在没有 owner 规则
时自动改写业务数据。

当前实现已提供三条 HTTP 路由、Mongo 持久化/唯一索引、compatibility hash、幂等 receipt、
OWNER 授权和 DRAFT/RUNNING 取消状态测试；下文标注的执行器和 publish 强制关联仍未完成。

## 计划模型

| 字段 | 约束 | 说明 |
| --- | --- | --- |
| `planId` | 服务生成、不可变 | 稳定 operation/reference，不能由客户端复用另一计划 |
| `tenantId`/`workspaceId` | exact scope | 所有读写均带双重租户边界 |
| `schemaId` | immutable | 计划只针对一个 schema family |
| `fromVersion` | 已发布、正整数 | 被迁移记录当前引用的版本 |
| `toVersion` | 已登记、正整数且大于 from | 目标 draft/published 版本 |
| `compatibility` | report + `compatibilityHash` | 创建时计算并固化，后续 schema 变化不改变计划结论 |
| `mode` | `DRY_RUN` 或 `APPLY` | DRY_RUN 只计数/采样；APPLY 才允许执行器 claim |
| `failureSampleLimit` | 1–100 | 失败样本上限，样本只能包含 record reference 和安全错误摘要 |
| `status` | `DRAFT`/`APPROVED`/`RUNNING`/`CANCEL_REQUESTED`/`CANCELLED`/`SUCCEEDED`/`FAILED` | 状态单向推进，取消是幂等请求 |
| `createdBy`/timestamps | `(iss, sub)` + UTC | 审计身份不使用 email/client name |

计划正文不保存转换脚本、原始记录 payload 或 token。转换逻辑由服务端已发布 mapping/
transformer 版本提供，客户端只能选择版本和模式。

## API 草案

```text
POST /api/v1/schemas/{schemaId}/migration-plans
GET  /api/v1/schemas/{schemaId}/migration-plans/{planId}
POST /api/v1/schemas/{schemaId}/migration-plans/{planId}/cancel
```

创建请求：

```json
{
  "tenantId": "tenant-1",
  "workspaceId": "workspace-1",
  "fromVersion": 1,
  "toVersion": 2,
  "mode": "DRY_RUN",
  "failureSampleLimit": 20
}
```

服务端创建时必须：

1. 以 OWNER/受权 operator 权限读取两个版本；`fromVersion` 必须是 PUBLISHED，目标版本不能
   低于源版本；
2. 调用 `CheckBackwardCompatibility` 并保存 canonical report/hash；
3. 若 report 为 breaking change，后续 publish/execute 必须引用该 `planId`，不能仅凭客户端
   再传一个布尔值绕过；
4. 以 `(tenantId, workspaceId, schemaId, fromVersion, toVersion, mode, compatibilityHash)`
   做幂等业务键，重复请求返回同一计划，冲突输入返回 409；
5. 在同一 Mongo transaction 写计划、operation receipt 和 audit entry。

取消请求只允许 OWNER/受权 operator，重复取消返回当前计划。已完成计划不可取消；RUNNING
计划先进入 `CANCEL_REQUESTED`，执行器在下一个 bounded checkpoint 变为 `CANCELLED`，不强杀
正在提交的单条记录事务。

## 执行与恢复不变量

- 每条记录携带 `planId` + `operationId`，用记录版本 CAS；失败样本只保留有限摘要。
- 计划 checkpoint、影响计数和 error budget 有上限；不承诺整个计划 exactly-once。
- `APPLY` 必须先有兼容性报告和明确目标 Schema；不允许对 PROJECTION owner 数据直接写回。
- 计划取消、失败、重试和完成均写 audit；旧 snapshot/history 只读不重写。
- 任何跨 workspace、schema family 或已撤销身份的访问均 fail closed。

## 验收门

P2-1-002 完成必须同时具备：API/OpenAPI、Mongo unique/index、幂等/冲突/取消状态机测试、
breaking 版本必须引用计划的 publish 负向测试，以及 bounded dry-run/失败样本证据。没有
真实 Mongo transaction 和恢复测试时只能标记 `PARTIAL`，不能宣称 migration 已完成。
