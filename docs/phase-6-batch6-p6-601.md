# Phase 6 Batch 6：P6-601 archive/retention/restore extension

P6-601 固化 archive、retention、legal hold、加密 backup/PITR 和 restore 验收边界。restore 只能
落到 disposable isolated service，并比较 count/content hash/index/schema/record version、cursor、
checkpoint、Inbox/Outbox、audit 和 backlog；projection rebuild 继续使用 staged replay + CAS pointer
switch，不能直接覆盖 live records，也不能删除审计或恢复证据。

机器可读 contract 位于 [`p6-601-retention-restore-spec.json`](../deploy/local/p6/p6-601-retention-restore-spec.json)，
验收结果位于 [`phase-6-retention-restore.json`](phase-6-retention-restore.json)。入口是 `make p6-601`。

已有 Phase 5 Mongo retention/PITR contract、Phase 3 backup/restore runbook、event archive、staged
rebuild 和 audit 边界；但本 Phase 6 的隔离 restore/retention live report 尚未提供，当前为
`BLOCKED_BY_LIVE_RESTORE_EVIDENCE`。
