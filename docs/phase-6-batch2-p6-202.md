# Phase 6 Batch 2：P6-202 workload identity lifecycle

P6-202 已审计现有 Dex/OIDC workload boundary：issuer、audience、subject、scope、tenant/workspace
和 owner/resource 都是精确 allowlist，wildcard 被拒绝。但当前代码没有 candidate/dual-accept/drain/revoke
状态机，因此结果标记为 `BLOCKED_BY_IDENTITY_LIFECYCLE_GAP`，不生成或旋转任何凭据。

机器可读 contract 位于 [`p6-202-workload-identity-spec.json`](../deploy/local/p6/p6-202-workload-identity-spec.json)，
盘点结果位于 [`phase-6-workload-identity.json`](phase-6-workload-identity.json)。入口是 `make p6-202`。

这是只读静态 gate（`liveTraffic=false`）。补齐并在四 owner 仓库验证 rotation/drain/revoke、审计和
故障 fixture 后，才能解除本项阻塞。
