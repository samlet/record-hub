# Phase 6 Batch 2：P6-203 connector fault matrix

P6-203 固化 duplicate、late result、owner restart、result relay outage、DLQ 和 rollback 六类故障，
要求 fixture digest、source commit、redaction、operator audit、rollback 和 liveStatus。当前只收集
静态 marker，缺少隔离 native topology 的 immutable live report，因此状态为
`BLOCKED_BY_LIVE_FAULT_EVIDENCE`，不会启用 connector。

机器可读 contract 位于 [`p6-203-fault-matrix-spec.json`](../deploy/local/p6/p6-203-fault-matrix-spec.json)，
盘点结果位于 [`phase-6-fault-matrix.json`](phase-6-fault-matrix.json)。入口是 `make p6-203`。

补齐六类 live case 并通过 P6-002 evidence contract 后，使用
`RECORD_HUB_P6_FAULT_REPORT=/path/to/report.json make p6-203` 复核。
