# P5-501 Production GA rollout preflight

P5-501 目前只实现 fail-closed rollout preflight，不启动生产流量。正式 rollout 必须以 P5-500 candidate PASS 为前置，采用 staged expansion，从一个获批 tenant/workspace 开始，覆盖完整 retry/retention/reconciliation observation window，保留明确且可审计的 rollback window，并记录 operator action 和 artifact digest。

静态入口：`make p5-501`。它验证 candidate prerequisite、staged expansion/audit、stop/rollback/legacy safety 和四仓库 commit boundary，并生成 `build/evidence/phase5/p5-501-*/p5-501.json`。当前状态为 `BLOCKED`，决策为 `DO_NOT_ROLLOUT`；该入口不调用部署系统，不修改生产配置。

解除条件：完成 P4-501、P5-G1～G7、P5-401 observation、P5-402 signoff 和 P5-500 immutable candidate 后，再由发布 owner 在隔离 rollout runner 中执行 staged expansion。任一 gate、SLO/RPO/RTO、sealed-data、重复副作用或 rollback 条件失败，都必须停止 publisher 并回到旧路径。
