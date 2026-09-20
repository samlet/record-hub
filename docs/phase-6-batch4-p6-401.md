# Phase 6 Batch 4：P6-401 Conductor binding

P6-401 固化 Conductor task 的 typed input/output、receipt polling、超时/重放规则和 owner-only side
effect。当前仅盘点 Bids Conductor workflow 与 Record Hub receipt boundary，缺 native task completion
 与 replay evidence，状态为 `BLOCKED_BY_LIVE_WORKFLOW_EVIDENCE`。

入口是 `make p6-401`，结果见 [`phase-6-conductor-binding.json`](phase-6-conductor-binding.json)。

原生证据采集入口为 `make p6-workflow-evidence`；它会启动第二个 Conductor diagnostic execution，
使用同一 operation ID 断言 snapshot ID/hash 不变且 `replayed=true`。该证据不会越过独立 P4/P5
GA 前置门禁，因此本任务报告仍保持 blocked。
