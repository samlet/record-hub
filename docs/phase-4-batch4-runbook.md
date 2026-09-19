# Phase 4 Batch 4 运维与安全 Runbook

## Owner 与触发

| 场景 | owner | 触发 | 首个证据 |
| --- | --- | --- | --- |
| credential/JWKS rotation | Approver + Record Hub | 到期、泄漏疑似、Dex key overlap | key version、active/overlap/expired counts |
| Mongo/PostgreSQL/NATS recovery | Record Hub/Bids/Approver | 服务不可用、lag 超阈值、DLQ 增长 | backup id、restore target、count/hash/index |
| approval reconciliation | 各业务 owner | VERSION_DRIFT、LATE_RESULT、unknown/dead 或 terminal mismatch | finding id、scope、generation/version |
| rollout/rollback | 发布 owner | SLO breach、跨租户拒绝失败、sealed-data scan 告警 | old/new artifact digest、flag、rollback point |

## 标准步骤

1. 记录 incident、tenant/workspace scope、actor、trace/request id 和当前 artifact/contract digest；暂停受影响 tenant 的新请求，不直接更新 terminal 状态。
2. 先查询 receipt/result、Inbox/Outbox、checkpoint 和 finding，确认远端是否已经成功应用；重复发送必须沿用原 `externalRequestId` 和 idempotency key。
3. 轮换 credential 时先创建新版本并验证 audience/scope/organization 映射，再启用 overlap；确认旧版本无 in-flight lease 后撤销旧版本。Dex/JWKS 遵循双 key overlap，不能删除仍在有效期内的 key。
4. 恢复数据时使用隔离 target，执行 count、hash、index、schema/migration version 和 cursor/checkpoint 校验；恢复后先以只读 worker 对账，再逐步放开 publisher。
5. 对账只允许调用 owner 的 reconciliation API/worker。`VERSION_DRIFT`、`LATE_RESULT`、scope mismatch、unknown/dead 必须产生 bounded finding；先修复映射或凭据，再按原 request 重试。

## 停止条件

立即停止并升级：任何跨 tenant/organization 读取或写入成功、出现 private key/token/PII/sealed bid payload、重复 terminal side effect、migration 不可逆、DLQ 无法安全重放，或恢复后的 count/hash/index/checkpoint 不一致。停止期间保持 feature flag OFF，保留旧 worker 和旧 artifact。

## 回滚与验收

回滚顺序为：停止新 publisher → 保留/导出 bounded evidence → 切回旧 artifact/worker → 恢复旧 feature flag → 对账未决 request → 只读 smoke → 小范围恢复。回滚不能删除 Inbox/Outbox、receipt、finding 或审计记录。只有负向矩阵、metrics 基数、evidence scan 和 recovery/reconciliation 证据全部通过，且隔离 live matrix 无 `SKIPPED`，才能进入 P4-501。

静态入口：`make p4-batch4`。当前 live 验收明确为 `SKIPPED`，原因是需要独立的四 owner + 数据库/消息/工作流拓扑和专用故障注入环境。
