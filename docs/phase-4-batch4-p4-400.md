# P4-400 跨租户负向矩阵

P4-400 固定四个 owner 的拒绝边界：请求方的 `tenantId`、Organization、workspace、identity 和资源引用必须同时属于当前安全上下文。跨 scope 请求统一返回 `403` 或通用无资源响应，不能通过不同响应体确认资源是否存在；结果应用还必须校验 request、resource、generation 和 organization 一致。

静态验收由 `scripts/verify-p4-batch4.sh` 的 `P4-400-negative-matrix` 执行，覆盖 Record Hub association、Fluxion tenant/workspace、Approver permission/service principal、Bids pending request/organization checks。跨四服务的真实身份矩阵需要隔离 tenant-a/tenant-b、organization-a/organization-b 和 Viewer/Editor/Operator 凭据，当前标记 `live=SKIPPED`。
