# P5-302 Settlement safe association/projection

Record Hub 新增 `settlement_associations` 安全读模型和只读查询入口 `GET /api/v1/associations/settlements`。模型只保存 tenant/workspace、稳定引用、organization、approval/settlement/action 状态、snapshot hash、版本、事件 hash、projection state 和时间；金额、银行账户、发票附件、sealed data 与 raw snapshot 从类型和 schema 上排除。

投影按 source version 单调推进：重复事件幂等，版本 gap/conflict 可见，晚到结果不能覆盖更高版本；Mongo 使用 scope unique/state indexes，API 继续走 workspace authorization 且没有写入口。

静态入口：`make p5-302`。它运行安全模型单测、scope/version boundary、Mongo index bootstrap、只读 endpoint 和 Phase 5 contract 检查。

当前状态为 `PARTIAL`：静态检查应全部 PASS；真实 Settlement event publisher、late-result finding、Mongo rebuild、cross-scope live matrix 和 operator console evidence 尚未执行，live 为 `SKIPPED`。

解除条件：提供 Settlement event/projection/rebuild topology 和 evidence root，设置 `RECORD_HUB_P5_SETTLEMENT_PROJECTION_LIVE=1` 后执行正常、重复、gap、conflict、late-result、跨 scope 和 rebuild 矩阵。
