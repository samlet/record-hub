# Phase 6 Batch 4：P6-400 Temporal binding

P6-400 固化 Temporal history 可保存的 typed ref/version/hash/operationId、四种事务属性以及
Activity timeout/retry/idempotency/hash assertion 规则。当前只做静态 adapter 盘点，缺 native Temporal
workflow 的 immutable history/replay evidence，状态为 `BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE`。

入口是 `make p6-400`，结果见 [`phase-6-temporal-binding.json`](phase-6-temporal-binding.json)。
