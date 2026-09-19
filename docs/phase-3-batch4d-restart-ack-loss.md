# Phase 3 Batch 4D：进程 restart / ACK-loss 矩阵

- 日期：2026-09-20
- 状态：P3-404 `DONE`
- 入口：`make p3-process-restart-ack-loss`

## 验收范围

Batch 4D 将已有真实 Fluxion command fault matrix 提升为独立 P3 gate。它不依赖 fake owner 或直接改
业务表，而是通过 Fluxion API 创建 project、提交真实 `project.annotate` command，并在以下边界注入
停止/竞争：

- owner transaction commit 后、JetStream ACK 前重投，Inbox/domain event/result outbox 只产生一次；
- Record Hub result publish 后、result outbox 标记 SENT 前重启，receipt 仍收敛；
- result consumer 暂停期间 owner 继续提交，恢复后 durable consumer 补齐；
- command 已 dispatch 时同时停止 Record Hub 和 Fluxion worker，再按顺序恢复；
- 两个 Record Hub result consumer 竞争同一 durable；
- malformed result 进入 safe DLQ 后，正常 follow-up command 继续推进。

入口脚本会保存底层 P3-110 原始日志和结果，再生成 `restart-ack-loss.json` 与
`p3-404-restart-ack-loss.json`。未设置 `RECORD_HUB_P3_RESTART_ACK_LIVE=1` 或缺少 live 依赖时安全
返回 `SKIPPED`。

## 边界说明

P3-404 的 command/result 故障矩阵已在真实 owner 进程上通过；Approver/Bids 各自领域命令尚无与
`project.annotate` 同等复杂的 ACK 注入入口，因此不把它们的业务副作用误写入本 gate。四 owner 的
启动、Temporal/Conductor workflow binding 和 NATS outbox recovery 由 P3-400～P3-403 独立证据覆盖。
