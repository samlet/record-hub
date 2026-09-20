# Phase 6 NATS native evidence collector

P6-402 的原生证据入口为：

```text
make p6-nats-event-evidence
```

collector 串联两个已存在的 native gate：

- P3-403：停止并恢复同一 JetStream file store，验证 owner outbox backlog、durable projection
  consumer、Inbox apply 和无重复副作用；
- P3-404：验证 commit-before-ACK、publish-before-SENT、consumer pause/restart、JetStream
  backlog、竞争消费和 poison DLQ redaction。

随后额外断言 `explicit ACK`、durable consumer、`MaxDeliver`/`MaxAckPending`、allowlisted event
subjects 和 recovery 后无 pending ACK。证据位于 `build/evidence/phase6/nats-event-live-<UTC>/`，
并生成 `p6-402-evidence.json` 与供 gate 使用的 `p6-402-live-report.json`。

该 collector 不改写 [`phase-6-nats-event.json`](phase-6-nats-event.json)，因为 Phase 6 仍受独立
P4/P5 GA 前置门禁约束；当前 native evidence 通过只代表 event/replay 证据已具备。
