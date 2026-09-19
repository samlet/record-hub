# Phase 3 验收报告：真实 workflow 业务系统接入

- 日期：2026-09-20
- 范围：Record Hub、Approver、Fluxion、Bids 的 P3-400～P3-409
- 报告状态：`DONE`（报告完整性门禁通过；不把 SKIPPED/PARTIAL 误判为 Beta 通过）
- 验收入口：`make p3-phase3-report`
- 报告提交：本文件加入的 P3-409 commit（以 `git log -- docs/phase-3-acceptance-report.md` 为准）

## 1. 结论

Phase 3 的隔离拓扑、真实 Temporal/Conductor workflow、NATS outage/recovery 以及
restart/ACK-loss 矩阵已完成真实本地验收。credential rotation、备份恢复和 rolling
upgrade/rollback 由于缺少可复现的 live 基础设施或旧版本工件，按验收规则保留为
`SKIPPED`；容量项仅完成 synthetic transport/projection baseline，保留为 `PARTIAL`。

因此本报告完成了 P3-409 的记录要求，但当前不能宣称四 owner 已达到完整 Beta readiness。
P3-405、P3-406、P3-407、P3-408 的重试条件见第 4 节；在这些条件满足前，不删除旧 worker、旧
schema reader 或 Fluxion 本地审批 fallback。

## 2. Gate 结果与提交基线

| ID | 状态 | 实现提交 | 证据/说明 |
| --- | --- | --- | --- |
| P3-400 | DONE | `5c38ab0` | 原生 Mongo/NATS/Dex/Temporal/Conductor 与四 owner 隔离启动；Darwin arm64 readiness PASS，manifest 记录四仓库 SHA |
| P3-401 | DONE | `5c38ab0`、`2fe3e01` | topology/owner/policy/schema/binding-policy fixture 生成，重复运行幂等，manifest hash 固定 |
| P3-402 | DONE | `2fe3e01` | Fluxion command→Temporal snapshot、Bids approval→Conductor snapshot、Mongo binding snapshot 与 Inbox/Outbox/history 断言 PASS |
| P3-403 | DONE | `467d207` | NATS outage 后复用同一 JetStream store；owner outbox、Mongo Inbox/APPLIED 与 source version 收敛 PASS；live evidence `record-hub-p3-four-owner-evidence.GtrV3H/nats-outage-recovery.json` |
| P3-404 | DONE | `d3c6591` | 真实 P3-110 fault matrix：commit-before-ACK、consumer pause/restart、cross restart、durable competition、DLQ follow-up 全部 PASS；live evidence `record-hub-p3-404-evidence.soZmDX/p3-404-restart-ack-loss.json` |
| P3-405 | SKIPPED | `427b56f` | OIDC JWKS 与 workload secret fail-closed contract PASS；四 owner 热轮换/JWKS overlap/credential reload 未实现，live SKIPPED；evidence `record-hub-p3-405-evidence.Nyyi1e/credential-rotation.json` |
| P3-406 | SKIPPED | `0abac2b`、`de9d3cb` | native dump/restore/archive gate 已交付；`de9d3cb` 修正为对恢复后的 Mongo/PostgreSQL/NATS 目标重新导出并比较 normalized manifests；未提供六个显式 source/isolated restore target，live SKIPPED；evidence `record-hub-p3-406-evidence.V6zml1/backup-restore.json` |
| P3-407 | PARTIAL | `c6d8e85` | 20 条 synthetic summary envelope 的真实 Mongo/NATS/projection baseline PASS；publish 23.202 events/s、drain 16.013 events/s、latency p50/p95/p99 21ms、max 22ms；四 owner mixed workflow load SKIPPED；evidence `record-hub-p3-407-evidence.Zl1FhN/capacity-metrics.json` |
| P3-408 | SKIPPED | `12c6c6c` | 全量 Go、command/result 与 approval contract mirror checks PASS；缺 previous/new worker artifacts、隔离升级目标和 rollback runner，live SKIPPED；evidence `record-hub-p3-408-evidence.u98qdZ/upgrade-rollback.json` |
| P3-409 | DONE | 本报告提交 | 本报告与 `verify-p3-phase3-report.sh` 完整性校验 PASS |

上述 `/var/folders/...` evidence 路径是本机临时运行目录，可能在系统清理后消失；仓库中的脚本、
任务表和本报告保留了可重复入口、状态、边界和重试条件。`c123.db` 等用户未跟踪文件不属于本
验收基线。

## 3. RPO/RTO 与容量解释

- P3-403 证明 JetStream durable backlog 可在 broker 重启后恢复并最终收敛；本批没有建立生产
  RPO/RTO 数字，故不能把该场景的恢复时长外推为 SLO。
- P3-406 尚未在持久 source/restore 目标上执行 Mongo/PostgreSQL/JetStream 全量恢复，备份
  RPO、恢复 RTO、hash/count/index/version 断言均待现场重试。
- P3-407 的 20-event 数值只描述当前机器、当前 projection 切片和 synthetic summary payload；
  不覆盖三 owner transactional outbox、Temporal/Conductor worker 并发、command/result backlog，
  也不构成生产容量承诺。

## 4. 遗留风险、owner 与重试条件

| 风险/待办 | Owner | 重试条件 |
| --- | --- | --- |
| workload/JWKS/secret 热轮换 | Platform/SRE + 四 owner | 提供可 reload 的 issuer、重叠 JWKS key、撤销窗口和 owner credential reload hook，再运行 `make p3-credential-rotation` |
| Mongo/PostgreSQL/JetStream 备份恢复 | Platform/SRE | 准备 disposable source 与 isolated restore targets，设置 `RECORD_HUB_P3_BACKUP_*`/`RECORD_HUB_P3_RESTORE_*` 六变量，再运行 `make p3-backup-restore` 并断言 count/hash/index/version |
| 四 owner 混合容量与 recovery | Workflow owners + Platform/SRE | 固定 50/200 msg/s 阶梯、worker 并发、hardware、retention/retry window、backlog drain SLO，在 P3-400 拓扑内重跑 `make p3-capacity-baseline` 的扩展场景 |
| rolling upgrade/rollback | Platform/SRE + release owners | 提供 immutable old/new worker/image、隔离 Mongo/PostgreSQL/NATS、feature-flag fixture 和 rollback runner，按 F3 顺序运行 `make p3-upgrade-rollback` |
| P3-308 三 owner live matrix | Approver/Fluxion/Bids owners | 同时启动可复现四应用隔离拓扑、Dex workload token 与领域命令入口，补齐真实业务副作用及 transaction rollback 证据 |
| P3-110 更细 transaction rollback/发布前崩溃 | Fluxion + Record Hub | 增加对应 fault injection boundary，验证无重复副作用、result terminal convergence 与 projection version |

## 5. 下一步准入判定

在 P3-405、P3-406、P3-408 仍为 `SKIPPED` 且 P3-407 为 `PARTIAL` 的情况下，下一阶段应继续
以 Beta-preparation 处理，不应关闭兼容 fallback 或宣称生产容量。完成表中重试条件并重新提交
对应 gate 后，才可重新评估完整 Beta readiness。

后续收口、真实审批切片和 Beta 发布门见 [Phase 4 Integration Beta 技术方案](phase-4-design.md)。
