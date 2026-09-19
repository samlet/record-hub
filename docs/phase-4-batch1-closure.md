# Phase 4 Batch 1：Phase 3 收口报告

- 日期：2026-09-20
- 状态：`PARTIAL`
- 入口：`make p4-batch1`
- 范围：P4-100～P4-106

## 本批完成

1. `tools/workload-issuer` 不再只能在启动时生成单个 signing key：它支持 `SIGHUP` 触发 client 文件 reload 和新 signing key 生成，JWKS 在 overlap 窗口同时暴露新旧 key，旧 key 到期后移除；移除 client 后旧 credential 立即 fail closed，token TTL 与 overlap 默认均为 5 分钟。
2. Batch 1 的 identity、六个独立 client direction、恢复组件/断言、mixed-owner 容量、expand-contract rollback 和 fault matrix 已固化在 [p4-batch1-spec.json](../deploy/local/p4/p4-batch1-spec.json)。
3. `make p4-batch1` 会先执行安全的静态/契约检查，并输出持久 evidence 目录中的 `batch1.json`；不会因缺少 live 依赖而伪造 PASS。

## 验证结果

| Gate | 静态/契约 | live | 说明 |
| --- | --- | --- | --- |
| P4-100 | PASS | SKIPPED | issuer rotation/reload 单测覆盖双 JWKS overlap、过期移除、client revoke |
| P4-101 | PASS | SKIPPED | 六个独立调用方向已进入 spec；四 owner secret reload 尚无 live hook 验收 |
| P4-102 | PASS | SKIPPED | 复用 P3 native dump/restore/archive runner；缺显式 disposable source/restore targets |
| P4-103 | PASS | SKIPPED | 规格固定 10k history、50/200 msg/s、100 pending、10 分钟 drain；mixed workflow load 未执行 |
| P4-104 | PASS | SKIPPED | expand-contract/old reader/flag/durable 规则已固定；缺 immutable old/new artifacts 与隔离升级目标 |
| P4-105 | PASS | SKIPPED | Fluxion 已加入 owner transaction rollback、publish-before-SENT crash、短 lease 与持久化证据；本机 pull consumer handoff 仍未形成可重复 live PASS |
| P4-106 | PASS | SKIPPED | contract mirror 通过；四 owner 同时运行的 live behavior matrix 未执行 |

在 2026-09-20 的一次显式 live 尝试中，聚合结果进一步细化为：P4-100、P4-101、P4-102、P4-104
仍为 `SKIPPED`；P4-103 为 `PARTIAL`（200 条 synthetic projection 的 P3-407 子 gate PASS，但
`fourOwnerMixedWorkload` 明确为 `SKIPPED`）；P4-106 为 `PARTIAL`（Fluxion restart/ACK-loss
子矩阵 PASS，但不是 Approver/Fluxion/Bids 三 owner 全矩阵）。原始临时证据目录为
`/tmp/record-hub-p4-batch1-live-final/`，可通过同一命令重建，不作为持久报告路径。

P4-105 的实现已落在 Fluxion：`FLUXION_RECORD_HUB_TEST_ROLLBACK_BEFORE_COMMIT` 在 owner
transaction 内制造回滚并硬停 worker，`FLUXION_RECORD_HUB_TEST_CRASH_BEFORE_RESULT_SENT`
在 publish 后、SENT 前硬停 relay；`FLUXION_RECORD_HUB_RESULT_OUTBOX_LEASE_SECONDS` 让恢复
窗口可控。P3-110 fault matrix 已生成对应断言，但本机 jnats pull durable 在硬停后的 handoff
仍不能稳定重放，故 live gate 暂记 `SKIPPED`，待独立 NATS/worker 拓扑复验。

默认运行会在 `build/evidence/phase4/batch1-<run-id>/` 保存报告；现场重试需显式设置：

```text
RECORD_HUB_P4_BATCH1_LIVE=1 make p4-batch1
```

该变量不会替代依赖准备。P4-102 仍要求六个 source/restore 环境变量，P4-104 仍要求 immutable old/new artifact 和隔离数据库/NATS 目标；缺少时必须保留 `SKIPPED`。

## 退出条件与下一步

Batch 1 不能作为 Beta readiness PASS，因为 P4-100/101/102/103/104/105/106 的 live 部分仍未完成。P4-105 的实现与静态契约已经完成，但当前本机 NATS pull consumer 在 owner 进程硬停后的 durable handoff 未形成稳定重放，因此保留 `SKIPPED`，不把失败尝试冒充 PASS。下一步应先准备可复现四 owner 隔离拓扑和 recovery/rollback 目标，再重跑本批；完成前不关闭旧 worker、旧 schema reader 或 Fluxion 本地 fallback。
