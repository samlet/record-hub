# P4-301：Bids approval Inbox/Outbox persistence

- 状态：`PARTIAL`（静态 PASS，live PostgreSQL upgrade/lease SKIPPED）
- Bids commit：`6fc875a`

Bids 新增 `0028_tender_approval_pilot.sql` 与等价 SQLite AutoMigrate 模型：request relay outbox、result inbox、Conductor task-completion outbox。external request、event、decision version 具备唯一约束，Store 测试覆盖组织隔离和幂等写入。
