# P5-301 Settlement confirmation request/result/Apply contract

Record Hub 现在 mirror 了 Approver 的 Settlement confirmation request/result v1 schema 与 fixture，并新增只表达安全 Apply evidence 的 schema。manifest 固定 source commit、schema/fixture SHA-256、event/schema version 和敏感字段排除规则。

边界保持三方所有权：Approver 负责 approval request/result 与 workflow decision，Settlement owner 负责 `settlement.confirm@v1` 的 Apply、幂等和财务事实，Record Hub 只保存安全 association/receipt/evidence；不写入金额、银行账户、发票附件或 sealed data，也不提供直接 Apply 入口。

静态入口：`make p5-301`。它比较两仓库 schema bytes/hash、检查 Approver request/result handler、验证 safe Apply schema 的 closed allowlist、scope/version/hash/idempotency contract。

当前状态为 `PARTIAL`：静态检查应全部 PASS；真实 Settlement owner Apply、Approver relay、late result/reconciliation、rollback 和跨系统 evidence 尚未执行，live 为 `SKIPPED`。

解除条件：提供 Settlement owner API/workflow、Approver result relay、Record Hub safe association 和隔离 evidence root，设置 `RECORD_HUB_P5_SETTLEMENT_LIVE=1` 后执行正常/拒绝/晚到/重复/版本冲突矩阵。
