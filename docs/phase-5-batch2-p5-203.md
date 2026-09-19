# P5-203 Production rotation 与 rolling rollback

本批次固化四 workflow owner 的发布顺序：先 additive migration 和兼容 reader，再以 flag off 发布新 producer/consumer，单 tenant 灰度，停止新流量并 drain 在途请求，最后才允许旧 artifact 回滚后的 contract 收紧。

静态入口：`make p5-203`。它检查 immutable old/new release artifact、expand/contract、feature flag/drain、idempotency/CAS no-double-write 边界和 rollback runbook。

当前状态为 `PARTIAL`：静态检查应全部 PASS；真实四 owner rolling upgrade、credential overlap、在途 drain、旧 artifact rollback 和 post-rollback reconciliation 尚未执行，live 状态为 `SKIPPED`。单一当前版本二进制不能冒充 old/new upgrade evidence。

解除条件：提供 old/new artifact root、隔离四 owner upgrade topology、operator credentials 和 evidence root，设置 `RECORD_HUB_P5_ROLLBACK_LIVE=1` 后按 manifest 顺序执行。任何 duplicate side-effect、orphan request、cursor 重建或 restore/count/hash 不一致都必须停止发布。
