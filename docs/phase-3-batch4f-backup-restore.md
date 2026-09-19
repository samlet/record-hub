# Phase 3 Batch 4F：Mongo / PostgreSQL / JetStream 备份恢复

- 日期：2026-09-20
- 状态：P3-406 `SKIPPED`（可执行 gate 已交付）
- 入口：`make p3-backup-restore`

## Gate 行为

`verify-p3-backup-restore.sh` 只接受显式 source/restore 目标，避免误操作本机共享服务：

- `mongodump --gzip` / `mongorestore --drop --gzip`；
- PostgreSQL `pg_dump --format=custom` / `pg_restore --clean --if-exists`；
- NATS JetStream file store tar archive/restore；
- backup/restore 文件 SHA-256 manifest。

所有 restore 目标必须由调用者明确指向 disposable isolated services；脚本不会推断数据库名，也不
会覆盖未声明的目录。

## 当前状态与重试条件

P3-400 supervisor 使用临时 Mongo dbpath、PostgreSQL databases 和 NATS store，并在退出时清理。为
避免在 gate 外保留敏感/业务数据，当前没有持久 source/restore 双拓扑，所以默认 live 结果为
`SKIPPED`，不是 PASS。证据将明确列出缺少的六个环境变量：

```text
RECORD_HUB_P3_BACKUP_MONGO_URI
RECORD_HUB_P3_BACKUP_PG_DATABASES
RECORD_HUB_P3_BACKUP_NATS_STORE
RECORD_HUB_P3_RESTORE_MONGO_URI
RECORD_HUB_P3_RESTORE_PG_DATABASES
RECORD_HUB_P3_RESTORE_NATS_STORE
```

下一次验收需在四 owner topology 运行期间创建临时副本/restore 目标，再比较 records、binding
snapshots、projection checkpoints/Inbox/audit、owner Inbox/Outbox、receipt 和 JetStream durable
cursor 的 count/hash/index/version；不能只比较 dump 文件存在。
