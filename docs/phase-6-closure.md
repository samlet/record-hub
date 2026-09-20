# Phase 6 静态聚合验收

Phase 6 的单任务 contract、fixture 和报告已经全部建立。`make p6-closure` 会读取
[`phase-6-task-breakdown.md`](phase-6-task-breakdown.md) 以及每个任务的 JSON 报告，检查任务状态和
报告状态是否一致，并输出 [`phase-6-closure.json`](phase-6-closure.json)。

当前聚合结果为 `PASS_STATIC_WITH_BLOCKERS`：静态报告一致，但 P6-000 仍受 Phase 5 前置阻塞，
多个真实 connector、workflow、NATS、restore、capacity、rollback 和多地域任务没有 native live
证据。聚合器不会把 `BLOCKED_*`、`SKIPPED` 或 `UNVERIFIED` 推断为 PASS，也不会启动新流量。

解除顺序：先清除 P5/P6-000，再补 P6-500/P6-501 的自助 control-plane 缺口和 SDK/identity 缺口，
最后在批准的原生拓扑中运行 connector、workflow、event、capacity、restore 和 rollback evidence。
