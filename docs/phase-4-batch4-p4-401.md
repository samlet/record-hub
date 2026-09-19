# P4-401 指标、SLO snapshot 与有限基数

所有指标只允许使用部署、consumer、connector、outcome 等有限基数标签，禁止 tenant、user、resource、request、event 和 hash 作为 label。最小可观测面包括 terminal outcome、materialization、delivery retry/dead、projection lag、backlog、DLQ、reconciliation finding 和 recovery outcome。

Record Hub 的 snapshot 同时提供 backlog、failure、lag age、SLO breach 与 error budget；Approver 暴露 delivery/reconciliation counters；Bids 暴露 outbox age/attempt 与 `/metrics`；Fluxion 的 approval 持久化和 finding 字段作为业务侧 materialization/reconciliation 采样源。静态检查由 `P4-401-metrics-slo` 执行，混合压测和告警阈值 live 验收当前 `SKIPPED`。
