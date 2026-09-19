# Phase 5 Batch 0：P5-002 Phase 4 live-gap re-audit

P5-002 是 Phase 5 的前置复核，不会启动服务、修改 P4 报告或解除阻塞。入口为
`make p5-002`，脚本会检查 P4-501 的五个 gate（P4-G1～P4-G5）和 fail-closed 约束，
并读取 `RECORD_HUB_P5_P4_REPORT` 或 Phase 4 evidence root 下最新的 `batch5.json`。

当前若缺少报告，或报告中任意 live case 为 `SKIPPED`、`PARTIAL`、`UNVERIFIED` 或 `PENDING`，
结果固定为 `BLOCKED_BY_P4`；不会把 Phase 4 的静态 PASS 当作 live PASS。
