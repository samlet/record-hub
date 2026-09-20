# Phase 6 Batch 4：P6-401 Conductor binding

P6-401 固化 Conductor task 的 typed input/output、receipt polling、超时/重放规则和 owner-only side
effect。当前仅盘点 Bids Conductor workflow 与 Record Hub receipt boundary，缺 native task completion
与 replay evidence，状态为 `BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE`。

入口是 `make p6-401`，结果见 [`phase-6-conductor-binding.json`](phase-6-conductor-binding.json)。
