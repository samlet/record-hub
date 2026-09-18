# Phase 3 live gate skip log

## P3-110 — Fluxion command E2E

- 状态：`SKIPPED`（2026-09-18）
- 已完成：Fluxion `V8` migration、strict command decoder、Inbox/Result Outbox、durable pull runner、
  `project.annotate` APPEND/VOID、tenant/workspace fail-closed mapping 和 result relay。
- 未完成：隔离 PostgreSQL、NATS JetStream、Record Hub API/worker、Fluxion API/worker 四进程同时启动，
  以及 commit-before-ACK、ACK loss、双 worker 竞争、两侧重启、DLQ/recovery 矩阵。
- 不以 fake server、内存仓库或直接改库替代 live Gate。
- 重试条件：准备专用端口/数据库/JetStream stream，加载
  `deploy/local/p3/fluxion-project-annotate-policy.json`，设置 Fluxion tenant/workspace 映射，
  然后执行 acceptance plan Gate B/C 全部 case 并保存 evidence directory。
