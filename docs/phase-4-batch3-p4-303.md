# P4-303：result apply 与单一审批 authority

- 状态：`PARTIAL`（静态 PASS，Conductor task retry/live SKIPPED）
- Bids commit：`8cad3ec`

ResultService 在同一数据库事务内写 result Inbox、校验 Tender organization/resource/generation/proposal/version、更新 Tender、发送 summary outbox，并写 Conductor task-completion outbox。`BIDS_EXTERNAL_TENDER_APPROVAL_ENABLED` 开启后 API 禁用本地 ApproveTender，外部 Approver 成为该 generation 的唯一 authority；worker dispatcher 复用 external request ID。
