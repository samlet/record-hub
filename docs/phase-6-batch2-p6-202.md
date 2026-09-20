# Phase 6 Batch 2：P6-202 workload identity lifecycle

P6-202 已审计现有 Dex/OIDC workload boundary：issuer、audience、subject、scope、tenant/workspace
和 owner/resource 都是精确 allowlist，wildcard 被拒绝。但当前代码没有 candidate/dual-accept/drain/revoke
状态机，因此结果标记为 `BLOCKED_BY_IDENTITY_LIFECYCLE_GAP`，不生成或旋转任何凭据。

机器可读 contract 位于 [`p6-202-workload-identity-spec.json`](../deploy/local/p6/p6-202-workload-identity-spec.json)，
盘点结果位于 [`phase-6-workload-identity.json`](phase-6-workload-identity.json)。入口是 `make p6-202`。

这是只读静态 gate（`liveTraffic=false`）。补齐并在四 owner 仓库验证 rotation/drain/revoke、审计和
故障 fixture 后，才能解除本项阻塞。

Record Hub 现在提供本地 fail-closed rotation service：`CREATE_CANDIDATE → DUAL_ACCEPT → DRAIN_OLD →
REVOKE_OLD`，拒绝 wildcard/跨 owner scope，撤销前要求 in-flight 为零，并以 hash-only immutable
audit 记录每次动作。该实现不生成或保存 token；四 owner 的真实 credential rotation、跨进程持久化和
native fixture 仍需独立验收。
