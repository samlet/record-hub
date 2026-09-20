# Phase 6 Batch 4：P6-402 NATS event/replay

P6-402 固化 registry-backed subject、durable double-ack、bounded replay、redacted DLQ、tenant ACL
和 backpressure。当前只盘点 Record Hub NATS runner/ACL marker，缺 native outage/replay immutable
evidence，状态为 `BLOCKED_BY_LIVE_EVENT_EVIDENCE`。

入口是 `make p6-402`，结果见 [`phase-6-nats-event.json`](phase-6-nats-event.json)。
