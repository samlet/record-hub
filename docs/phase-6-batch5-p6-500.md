# Phase 6 Batch 5：P6-500 self-service control plane

P6-500 固化 Web/API control plane 的边界：表格、schema、受控 tag、typed relation、view 和
安全导出的操作都必须带 tenant/workspace scope，并按 Viewer、Editor、Operator、Owner 的
动作矩阵授权。未知动作隐藏并由服务端拒绝；拒绝、变更和导出均写 immutable audit，导出只能
读取已发布 view/schema 的字段并执行 redaction。

机器可读 contract 位于 [`p6-500-control-plane-spec.json`](../deploy/local/p6/p6-500-control-plane-spec.json)，
验收结果位于 [`phase-6-control-plane.json`](phase-6-control-plane.json)。入口是 `make p6-500`。

当前静态 inventory 确认已有 workspace/table/schema/record/view API、typed relation 数据模型、
RBAC 和 audit 基础；受控 tag dictionary 的 ID、敏感前缀、层级 scope、互斥 group 和 assignment
校验已固化在 `server/internal/modules/records/tags.go`。但 dictionary Mongo/API 接入和 relation
专用 console 仍未实现，故报告仍为 `BLOCKED_BY_CONTROL_PLANE_GAP`，禁止启用新的自助化
mutation/export 流量。补齐缺口后还必须提供 API/UI role matrix、跨 scope negative matrix 和
redacted export receipt 才能进入 live gate。
