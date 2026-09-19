# Phase 3 live gate skip log

## P3-110 — Fluxion command E2E

- 初始状态：`SKIPPED`（2026-09-18）；已由 2026-09-19 的核心 live slice 取代为 `PARTIAL`。
- 已通过：`make p3-fluxion-command-live` 使用隔离 Mongo replica set、NATS JetStream、Temporal、临时
  PostgreSQL、Record Hub `all` 和 Fluxion API/worker；项目通过 Fluxion API 建立，真实验证 APPEND、VOID、
  相同 operation replay、hash conflict、过期 version 拒绝、Inbox/Result Outbox、summary version +2、
  Record Hub projection source version 收敛。
- 证据：脚本每次运行生成 `manifest.json`、脱敏 `config-redacted.json`、`results.json`、日志和数据库断言；
  最近一次核心运行的结果为 PASS（Darwin arm64，Record Hub `942092b` 基线、Fluxion `7811ddd`，运行约 21 秒）。
- 故障矩阵入口已实现：`make p3-fluxion-command-faults` 会在同一隔离拓扑中启动第二个 Fluxion worker，
  使用 Fluxion 测试故障点验证 commit-before-ACK 重放，并暂停 Record Hub result consumer 验证 durable
  result 收敛；每次运行继续生成相同格式的 evidence directory。
- 尚未覆盖：跨系统两侧同时重启；NATS outage/backlog recovery 和 result poison DLQ 已由故障矩阵覆盖；这些保持为后续批次的 `TODO`，
  不能据此宣称 Phase 3 全部 Gate 通过。
- 不以 fake server、内存仓库或直接写业务库替代 live Gate；脚本只使用数据库读查询做断言。
- 后续重试条件：在本 gate 基础上加入故障注入与重启控制，继续使用
  `deploy/local/p3/fluxion-project-annotate-policy.json` 和 tenant/workspace 精确映射，保存同一格式的
  evidence directory。
