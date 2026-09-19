# Phase 4 Batch 0 基线验收

- 日期：2026-09-20
- 状态：DONE
- 范围：P4-001、P4-002、P4-003、P4-004
- 阶段边界：本批固定契约、证据格式和可重复 fixture；不宣称真实 Beta 流量或 live topology 已完成。

## 结果

| Gate | 命令 | 结果 | 证据 |
| --- | --- | --- | --- |
| P4-001 | `make p4-baseline` | PASS | [phase-4-baseline-manifest.json](phase-4-baseline-manifest.json) |
| P4-002 | `make p4-contract-inventory` | PASS | [phase-4-contract-inventory.json](phase-4-contract-inventory.json) |
| P4-003 | `make p4-evidence-contract` | PASS | `contracts/evidence/`、`build/evidence/phase4/<run-id>` 布局 |
| P4-004 | `make p4-fixtures` | PASS | `deploy/local/p4/p4-fixture-spec.json`、`scripts/bootstrap-p4-fixtures.sh` |

## 跨仓库快照

P4-001 基线捕获的四仓库 commit 如下；外部仓库的 manifest hash 修复已经分别提交并推送：

| 仓库 | commit | 说明 |
| --- | --- | --- |
| Record Hub | `0e86f71e2373e281391bd60e5c771374f8814f19` | Batch 0 gates 实现基线 |
| Approver | `83c1cb145a9a417a8a69262867bd108a6d308904` | application summary schema hash 刷新 |
| Fluxion | `9349638f1157d190752107bf54ede3691e253d87` | project summary schema hash 刷新 |
| Bids | `aae52703bdaf93d69fc136674ebb289cabef7867` | tender summary schema hash 刷新 |

三处外部变更只修正 summary manifest 中过期的 `schemaContentHash`；schema 文件本身与 Record Hub canonical bytes 一致。对应远端已经推送到各自 Gitee `main`。

P4-001 实际构建并记录了 Record Hub、Approver API/worker、Fluxion server、Bids API/worker 共 6 个 artifact 的 bytes 和 SHA-256；完整值以 baseline manifest 为准。测试命令已固定在 manifest，但本门只执行构建，不把未执行的 live/test 结果标为 PASS。

## Evidence 与 fixtures

- P4-003 的 evidence manifest schema 使用 `urn:record-hub:phase4-evidence-manifest:v1`，拒绝绝对路径、路径穿越和非 `PASS|PARTIAL|SKIPPED|FAIL` 状态；Go contract test 同时验证 valid/invalid fixture。
- P4-004 fixture 默认将 `fluxion.approval.external` 与 `bids.approval.external` 关闭，并固定 tenant/workspace/organization、approval slice、Mongo/Postgres/JetStream recovery target 和 expand-contract release window。
- 生成目录默认是 `.runtime/p4/fixtures`；通过 `RECORD_HUB_P4_FIXTURE_DIR` 可写入 disposable 目录。fixture manifest 只包含规范化文件 hash，不含密钥、时间戳或随机业务数据。

## 限制与后续

- 当前验收不包含真实 Mongo/NATS/Dex/Temporal/Conductor 拓扑、跨系统 live request/result、故障注入或生产发布。
- `c123.db` 是工作区既有未跟踪文件，已保留且未纳入提交。
- 下一批从 Batch 1 开始，优先完成 reloadable workload identity、恢复集合、升级/回滚 harness 与 P3 live closure；在此之前 Phase 4 Beta flags 保持 OFF。
