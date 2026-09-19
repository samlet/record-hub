# Phase 4 Batch 2：P4-204 Project↔Application typed association projection/API

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：Record Hub
- 前置：[P4-002 contract inventory](phase-4-contract-inventory.json)、[P4-201 state machine](phase-4-batch2-p4-201.md)

## 交付边界

Record Hub 新增 `project_application_associations` Mongo read model，以及只读接口
`GET /api/v1/associations/project-applications`。投影只接收已通过 approval summary v1 allowlist 的字段：
typed `fluxion:PROJECT:*` / `approver:APPLICATION:*` refs、workflow stable refs、approval status、proposal hash、
generation、decision version 和 source version；不会复制表单、客户、候选人、地址、文件或其他 workflow payload。

每个 `(tenantId, workspaceId, projectRef, applicationRef)` 只有一个稳定 association ID。投影按 source version 单调前进：
旧事件幂等忽略，跳版本标记 `GAP` 并等待补发，重复版本不同 hash 标记 `CONFLICT` 并停止自动覆盖。API 在服务边界
先做 workspace membership 授权，再按 tenant/workspace 过滤查询；Viewer 也只能读取自己有 membership 的 scope。

## 验证

| 检查 | 结果 | 说明 |
| --- | --- | --- |
| typed association model/repository tests | PASS | stable ID、版本单调、gap repair、same-version conflict |
| summary projector integration test | PASS | approval summary allowlist 接入 association writer |
| HTTP auth/safe-field tests | PASS | tenant/workspace scope、Viewer read、cross-tenant 403、无敏感 payload |
| Go full test + OpenAPI lint | PASS | `go test ./...`、`go run ./tools/openapi-lint api/openapi.yaml` |
| Mongo/NATS live projection and rebuild | SKIPPED | 当前未启动隔离 Mongo/NATS topology；不以静态单测冒充 live PASS |

P4-204 不提供写 association 的用户 API；写入权只属于 Record Hub projection worker。
