# Phase 6 Batch 5：P6-503 operator runbook and rollback

P6-503 固化 connector/event/table 事故处理顺序：停止新流量、保存安全证据、drain 在途、禁用
connector、使用原始 identity 重放、对账 receipt/finding、切回旧 artifact、只读 smoke，再恢复
小范围流量。任何跨 scope、重复终态副作用、敏感正文泄漏、无界 backlog/DLQ、hash/checkpoint
不一致或缺少回滚 artifact 都必须停机升级。

机器可读 contract 位于 [`p6-503-operator-runbook-spec.json`](../deploy/local/p6/p6-503-operator-runbook-spec.json)，
验收结果位于 [`phase-6-operator-runbook.json`](phase-6-operator-runbook.json)。入口是 `make p6-503`。

安全预检入口是 `make p6-503-dry-run`，输出到被忽略的
`build/evidence/phase6/p6-503-dry-run.json`。它只检查 procedure 顺序、scope/replay/rollback
边界和可选的 redacted evidence envelope；不会停止服务、排空队列、禁用 connector、重放事件或
切换 artifact。可用 `RECORD_HUB_P6_RUNBOOK_DRY_RUN_REPORT=/path/to/redacted.json` 对一份候选
证据做结构与敏感字段预检，但即使预检通过，也不会将 live rollback 状态改为 PASS。

静态 runbook、connector disable/drain、rebuild/audit 和安全 evidence 边界已存在；但真实的
隔离 native stop/drain/replay/disable/rollback drill 尚未提供 immutable report，因此状态为
`BLOCKED_BY_LIVE_ROLLBACK_EVIDENCE`，不会自动执行任何破坏性操作。
