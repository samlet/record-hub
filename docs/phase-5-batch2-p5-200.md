# P5-200 Mongo HA、index、retention 与 PITR

本批次先固化 Mongo 生产化 contract：至少三成员 replica set、majority read/write concern、加密 backup/PITR、restore 后 count/hash/index/version/cursor/backlog assertion，以及显式 retention policy。静态实现继续复用 Record Hub 已有的 transaction、CAS、change stream、projection event archive、cursor expiry 和统一 `ensureMongoIndexes` bootstrap。

静态入口：`make p5-200`。它检查本地 replica-set 配置、所有 Mongo adapter 的 index bootstrap、replay/retention 边界、事务/游标测试和 Phase 5 data/ops contract。

当前状态为 `PARTIAL`：静态检查应全部 PASS；真实 replica-set stepdown、备份/PITR restore、retention expiry、count/hash/index/version/cursor/backlog 对比尚未执行，live 状态为 `SKIPPED`。本地 Homebrew 单节点或普通共享 Mongo 不能替代生产-like 隔离拓扑。

解除条件：由平台 owner 提供独立 Mongo replica set、加密 backup/PITR target、restore runner 和不可变 evidence root，设置 `RECORD_HUB_P5_MONGO_LIVE=1` 后重新执行 gate。任何 restore assertion、RPO/RTO 或 retention 结果缺失都不能标记 PASS。
