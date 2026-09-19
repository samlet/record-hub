# Phase 4 Batch 2：P4-205 Fluxion Approval Beta fault matrix

- 日期：2026-09-20
- 状态：`PARTIAL`
- owner：四仓库
- 前置：P4-200..P4-204
- Record Hub commit：`bc82419`（`test(phase4): add batch2 fault matrix gate`）
- 规范：[p4-batch2-spec.json](../deploy/local/p4/p4-batch2-spec.json)

## 矩阵入口

`make p4-batch2`（或直接运行 `scripts/verify-p4-batch2.sh`）生成
`build/evidence/phase4/batch2-<run-id>/batch2.json`。入口不写入业务数据库，也不连接用户机器上的普通
MongoDB/NATS/Temporal；它只验证四仓库代码中的稳定 request/outbox、restart、JetStream durable、late-result
reconciliation 和 tenant/workspace flag/drain 契约，并记录三个仓库当前 commit。

| Case | 静态 contract | live gate | 说明 |
| --- | --- | --- | --- |
| response loss | PASS | SKIPPED | stable request/result Inbox/outbox 可重试；需要隔离服务注入响应丢失 |
| restart | PASS | SKIPPED | P3 restart/ACK-loss runner 与 owner Inbox 入口存在；需要 Batch 2 专用 topology |
| NATS outage | PASS | SKIPPED | durable JetStream/outbox recovery runner 已存在；未启动隔离 broker |
| late result | PASS | SKIPPED | Fluxion generation guard 与 Approver VERSION_DRIFT/LATE_RESULT 已落地 |
| flag rollback | PASS | SKIPPED | tenant/workspace rollout snapshot、in-flight drain 和 local fallback 已落地 |

脚本最终状态为 `PARTIAL`，因为所有静态检查通过而 live cases 未执行。设置
`RECORD_HUB_P4_BATCH2_LIVE=1` 只会在未来接入显式隔离 evidence runner 后改变 live gate；当前不会把普通本地
服务误报为 PASS。P4-501 仍要求这些 live 必选项全部通过。
