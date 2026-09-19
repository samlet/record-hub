# P5-102 Viewer / Editor / Operator / Admin 权限分离

Record Hub 现已增加独立 `OPERATOR` workspace role 及 `operations.read`、`projection.manage` actions。投影运维 snapshot 和 command operations 不再复用普通 workspace read；projection rebuild 的 start/cancel 使用 `projection.manage`，读取使用 `operations.read`；`projection.write` 仍不授予任何人工角色，只能由 worker 使用。

审批关联 console 保持只读，Viewer/Operator 只能查询安全关联，POST/PATCH/DELETE 由 HTTP handler 返回 405，跨 scope 返回 403。静态入口：`make p5-102`，角色模型、运维边界、回归测试、只读控制台和 Phase 5 contract 均 PASS。

生产 role-bound identity、API/UI/operator browser matrix 和 audit export 仍需 live 验收，当前为 `SKIPPED`。
