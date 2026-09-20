# Phase 6 Batch 5：P6-502 observation/reconciliation dashboard

P6-502 固化运维观察面板的 scope 和信号：lag、backlog、retry、dead、finding、recovery 都必须
按 tenant/workspace 读取，维度固定且有界，snapshot 超过 freshness window 要标记 stale 并告警。
面板只读，raw payload 和高基数标签禁止进入 metrics/export；导出必须产生 operator audit。

机器可读 contract 位于 [`p6-502-observation-dashboard-spec.json`](../deploy/local/p6/p6-502-observation-dashboard-spec.json)，
验收结果位于 [`phase-6-observation-dashboard.json`](phase-6-observation-dashboard.json)。入口是 `make p6-502`。

当前已有 projection operations snapshot、bounded metrics、retry/DLQ、rebuild 和只读 Operations
console；本批已将 checkpoint GAP/FAILED 与 inbox reject/fail 汇总为 bounded findingCount，补充
recoveryAgeSeconds、stale freshness 字段/metrics，并在 Web 运维面板展示 finding/reconciliation
与恢复年龄。静态结果为 `PASS_STATIC`，但仍需 native recovery evidence、完整审计导出和 Phase 5
独立门禁，不能启用新的 dashboard/export live 流量。
