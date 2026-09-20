# Phase 6 Batch 4：P6-403 event-to-table mapping

P6-403 固化 registry mapping、source pointer、单调版本、duplicate/gap/conflict/hash mismatch 处理和
staged rebuild pointer switch。当前只盘点现有 projector/catalog/rebuild boundary，缺 native event replay
与 rebuild immutable evidence，状态为 `BLOCKED_BY_LIVE_EVENT_EVIDENCE`。

入口是 `make p6-403`，结果见 [`phase-6-event-table.json`](phase-6-event-table.json)。
