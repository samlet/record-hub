# Phase 5 Batch 2 收口：HA data/event 与灾备

## 结论

Batch 2 的生产化 contract、静态验证入口和 live 前置条件已经固化；四项均为 `PARTIAL`，因为真实生产-like topology、故障注入、restore 和容量证据尚未具备，不能作为 Production GA gate PASS。

| 任务 | 提交 | 静态结果 | Live 状态 |
| --- | --- | --- | --- |
| P5-200 Mongo HA/index/retention/PITR | `4cbf1b8` | replica set、index bootstrap、retention/replay、事务/cursor contract 全 PASS | SKIPPED：缺隔离 replica set、加密 PITR/restore runner、不可变 evidence |
| P5-201 JetStream stream/consumer HA | `79053b6` | stream、durable consumer、explicit ACK、MaxDeliver、DLQ/replay、owner outbox contract 全 PASS | SKIPPED：缺三节点 JetStream、四方 owner fault harness |
| P5-202 capacity/SLO/RPO/RTO | `88dbc1f` | capacity runner、bounded metrics、recovery contract、四 owner boundary 全 PASS | SKIPPED：缺 mixed-load topology、dashboard export、签字 RPO/RTO |
| P5-203 rotation/rolling rollback | `f83b634` | immutable artifact、expand/contract、drain、no-double-write、rollback runbook 全 PASS | SKIPPED：缺 old/new artifacts、隔离升级目标、rollback runner |

## 不可替代原则

- 本地 Homebrew 单节点 Mongo 和单节点 NATS 只证明开发 smoke，不能证明 replica-set/JetStream HA。
- synthetic capacity 或单 owner fault evidence 不能外推为四 owner Production GA 目标。
- `SKIPPED`、`UNVERIFIED` 或缺失 owner sign-off 的 RPO/RTO 不得写成达标。
- old/new artifact 必须可寻址且 immutable；不能用同一当前版本二进制冒充 rolling rollback。

## 下一批前置

1. P4-501 live gate 仍需先完成；Phase 5 不进入 GA 流量。
2. 由平台 owner 提供 Mongo/NATS HA、backup/PITR、mixed-load、old/new release 和 evidence root。
3. Batch 3 将处理 connector registry、Settlement confirmation contract、安全 association/projection，以及 Fluxion/Bids owner hardening；未知 connector 和敏感 Settlement 字段必须 fail closed。

## 验证记录

- `go test ./...`：PASS。
- `make p5-200`、`make p5-201`、`make p5-202`、`make p5-203`：静态检查全部 PASS；live 按前置条件 SKIPPED，整体状态 `PARTIAL`。
- 用户已有未跟踪本地文件 `c123.db` 未纳入提交。
