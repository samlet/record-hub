# Phase 6 Batch 2：P6-201 SDK parity inventory

P6-201 先完成跨仓库 source-marker inventory，检查 typed ref、command/result、receipt 和 error
四个 facet。当前结果不会伪装成 SDK parity：缺少任一必需 facet 会输出 `BLOCKED_BY_SDK_GAP`，
并禁止发布新的统一 SDK artifact。

机器可读 contract 位于 [`p6-201-sdk-parity-spec.json`](../deploy/local/p6/p6-201-sdk-parity-spec.json)，
盘点结果位于 [`phase-6-sdk-parity.json`](phase-6-sdk-parity.json)。入口是 `make p6-201`。

当前只做只读盘点（`liveTraffic=false`）；还需要在缺口仓库补齐 typed ref、command/result、receipt、
errors 的编译/契约测试后，才能转为 PASS 并进入真实 connector 工作。

Record Hub 本地 Go/Java SDK 已补齐 metadata-only `RecordRef`、`CommandResult`、`Receipt` 和
可扩展 error-code marker，并新增 TypeScript parity types；P6-201 仍不会因此解除，因为
Approver typed-ref、Bids Java typed-ref/command-result 等跨仓库缺口仍需各 owner 独立提交和编译证据。
