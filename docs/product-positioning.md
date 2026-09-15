# 产品定位与 Supabase 对比

## 1. 定位

Record Hub 定位为：

> 面向长运行 Workflow、企业集成和多维数据协作的 workflow-native backend/data platform。

它可以在“一个 SDK 同时获得数据、认证、实时事件和管理 UI”的开发体验上逐步对标 Supabase，但首期不是 Supabase clone，也不宣称能力等价。

## 2. 能力对比

| 维度 | Supabase | Record Hub 目标 | 当前状态 |
| --- | --- | --- | --- |
| 核心数据 | 每项目 PostgreSQL | MongoDB 动态 Record + 外部系统投影 | 设计中 |
| 数据 API | 自动 REST/GraphQL API | Schema 驱动的受控 Record API | 设计中 |
| Auth | 集成 Auth 服务与用户管理 | Dex OIDC + 本地 membership/policy | 设计中 |
| 数据授权 | PostgreSQL grants/RLS | API 层 tenant/row/field policy | 设计中，不等价于数据库 RLS |
| Realtime | DB changes、Broadcast、Presence | JetStream 后端事件 + WebSocket/SSE Gateway | JetStream 已选，Gateway 未实现 |
| Storage | 集成对象存储 API | 外部对象存储引用与后续 Storage Gateway | 非首期 |
| Functions | Edge Functions | Workflow Activity/Worker 与后续 sandbox function | 非首期 |
| Dashboard | Studio | 多维表格、Schema、事件、Binding 控制台 | 设计中 |
| SDK | 多语言 client | Java、Go、TypeScript 生成 client | 计划中 |
| Workflow | 非核心能力 | Temporal/Conductor Binding、快照、receipt、Saga | 核心差异化 |
| 跨系统投影 | 不是主要定位 | Approver/Fluxion/Bids 状态速查 | 核心差异化 |

## 3. 不能直接宣称“已对标”的原因

MongoDB、NATS 和 Dex 解决的是底层存储、消息和身份认证，并不会自动产生：

- 安全的自动数据 API；
- 完整用户管理、邀请、找回、MFA 和管理控制台；
- 类似数据库 RLS 的强制授权；
- 面向浏览器的 Realtime Broadcast 和 Presence；
- 对象存储、签名 URL、转换和生命周期；
- Functions runtime、部署和隔离；
- 项目创建、配额、备份、恢复、日志、指标和计费控制面；
- 完整多语言 SDK、CLI 和本地开发体验。

因此阶段性表述应为：

```text
Phase 1-3: workflow data middleware / integration hub
Phase 4+:  workflow-native backend platform
成熟后:    在目标场景的开发体验上对标 Supabase
```

## 4. 差异化方向

Record Hub 不应在通用 PostgreSQL BaaS 上正面复制 Supabase，而应突出：

1. 多维表格是主要业务界面，不只是数据库管理器。
2. 原生支持 Temporal 与 Conductor 的 Workflow Binding。
3. 固化 snapshot/version/hash、幂等 receipt 和 Saga 语义。
4. 统一投影多个既有业务系统，而不是要求业务数据迁入平台。
5. Approver 是原生审批服务，可与动态记录和流程节点关联。
6. Schema.org 语义映射和企业领域 Schema 并存。

## 5. 演进门槛

只有至少完成以下能力后，才适合在产品说明中使用“BaaS”而不仅是“中间件”：

- 稳定 Data API 和三语言 SDK；
- 完整用户生命周期与租户管理；
- 经负向测试的 row/field policy engine；
- 浏览器 Realtime Gateway；
- 备份恢复、配额、审计和管理控制面；
- 明确的版本兼容与迁移承诺。

