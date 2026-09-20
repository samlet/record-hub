# Phase 6 Batch 3：P6-303 Settlement safe association

P6-303 固化 Settlement confirmation request/result/Apply 边界。Record Hub 只保存稳定引用、版本、
snapshot hash 和 receipt/association；金额、银行、发票附件和 sealed data 继续由 Settlement owner
持有。当前缺跨进程 Apply 的 native immutable live evidence，状态为 `BLOCKED_BY_LIVE_CONNECTOR`。

入口是 `make p6-303`，结果见 [`phase-6-settlement-association.json`](phase-6-settlement-association.json)。
