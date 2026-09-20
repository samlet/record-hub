# Phase 6 Batch 0：P6-001 contract/schema inventory

P6-001 已完成，只读取四个真实仓库的 source contract/schema，固定 owner、family、kind、schemaVersion、SHA-256
和 source commit，生成 [`phase-6-connector-schema-inventory.json`](phase-6-connector-schema-inventory.json)。
生成 classes、build/target 输出和未跟踪文件被排除。

入口是 `make p6-001`。本门 `PASS` 只代表 inventory 完整，不启用 connector、不发布 NATS 消息、不启动
Temporal/Conductor workflow，也不解除 Phase 5 的生产前置条件。
