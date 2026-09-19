# P4-404 Rotation、recovery 与 reconciliation runbook

批次 runbook 将 owner、触发条件、停止条件、回滚步骤、重放/对账入口和升级路径集中在 [phase-4-batch4-runbook.md](phase-4-batch4-runbook.md)。原有 credential rotation、backup/restore、capacity 和 upgrade/rollback runbook 继续作为具体执行步骤；没有 source/restore target 或隔离身份时不得把静态通过写成 live 通过。
