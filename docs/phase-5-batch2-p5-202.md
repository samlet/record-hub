# P5-202 Capacity、SLO、RPO/RTO drill

本批次固化四 owner 的容量与恢复测量基线：10,000 command operations、1,000 并发在途、每 owner 10,000 Inbox/Outbox 历史、steady 50 msg/s、burst 200 msg/s、100 个并发审批，以及 consumer 停止 10 分钟后的 backlog drain。指标至少覆盖 terminal latency p50/p95/p99、backlog、lag、retry/dead、finding、availability 和 error budget。

静态入口：`make p5-202`。它检查既有 capacity runner、bounded metrics、RPO/RTO contract、四 owner 边界和“不以 UNVERIFIED 冒充达标”的规则。

当前状态为 `PARTIAL`：静态检查应全部 PASS；四 owner mixed workflow load、资源增长、backlog drain、Mongo/NATS restore、RPO/RTO 和签字证据尚未执行，live 状态为 `SKIPPED`。P3/P4 的 synthetic 或单 owner evidence 不能外推为 Phase 5 Production GA 容量。

解除条件：平台 owner 提供隔离四 owner topology、可导出的 metrics/dashboard、capacity/fault runner 和 evidence root，设置 `RECORD_HUB_P5_CAPACITY_LIVE=1` 后重新执行；任何缺失的实测目标必须保留为 `UNVERIFIED`。
