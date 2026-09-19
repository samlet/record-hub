# P4-302：request relay / materializer / result dispatcher

- 状态：`PARTIAL`（静态 PASS，跨服务 response-loss live SKIPPED）
- Bids commit：`8cad3ec`
- Approver commit：`41ae520`

Bids worker 在 prepare 事务中创建稳定 `bapr-*` request，HTTP relay 使用 `Idempotency-Key`、lease/retry/backoff；Approver 具备 Bids request validator/result contract。Approver→Bids result endpoint 由 token 保护，重复终态按 event/version/hash 幂等。
