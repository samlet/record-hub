# Phase 6 Batch 4：P6-402 NATS event/replay

P6-402 固化 registry-backed subject、durable double-ack、bounded replay、redacted DLQ、tenant ACL
和 backpressure。当前只盘点 Record Hub NATS runner/ACL marker，缺 native outage/replay immutable
 evidence，状态为 `BLOCKED_BY_LIVE_EVENT_EVIDENCE`。

入口是 `make p6-402`，结果见 [`phase-6-nats-event.json`](phase-6-nats-event.json)。

原生证据采集入口为 `make p6-nats-event-evidence`；它组合 P3-403 outage/backlog recovery 与
P3-404 ACK-loss/replay/DLQ fault matrix，并额外断言 durable consumer、explicit ACK、allowlisted
subjects 和 recovery 后 pending ACK 为零。证据通过也不会越过独立 P4/P5 GA 前置门禁，因此本任务
报告仍保持 blocked。
