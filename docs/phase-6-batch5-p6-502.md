# Phase 6 Batch 5：P6-502 observation/reconciliation dashboard

P6-502 固化运维观察面板的 scope 和信号：lag、backlog、retry、dead、finding、recovery 都必须
按 tenant/workspace 读取，维度固定且有界，snapshot 超过 freshness window 要标记 stale 并告警。
面板只读，raw payload 和高基数标签禁止进入 metrics/export；导出必须产生 operator audit。

机器可读 contract 位于 [`p6-502-observation-dashboard-spec.json`](../deploy/local/p6/p6-502-observation-dashboard-spec.json)，
验收结果位于 [`phase-6-observation-dashboard.json`](phase-6-observation-dashboard.json)。入口是 `make p6-502`。

当前已有 projection operations snapshot、bounded metrics、retry/DLQ、rebuild 和只读 Operations
console；但 finding/reconciliation 指标与恢复态面板尚未形成统一实现，状态为
`BLOCKED_BY_OBSERVABILITY_GAP`，不启用新的 dashboard/export 流量。
