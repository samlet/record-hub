# P4-503 Phase 4 Beta 验收报告模板

本文件是最终报告的受控模板；在 P4-501/P4-502 未通过前，不得填写为 PASS 或签字。

## 版本与拓扑

| 项目 | 结果 |
| --- | --- |
| RC manifest | `docs/phase-4-release-candidate-manifest.json` |
| 四仓库 commit/artifact | 从 RC manifest 原样引用，禁止手工改写 |
| 实际拓扑/硬件 | `UNVERIFIED` |
| evidence retention | 待发布前确认 |
| P4-G1～G5 | `SKIPPED` |

## 验收摘要

| 维度 | 目标 | 实测 | 状态 |
| --- | --- | --- | --- |
| rotation/JWKS overlap | 旧新 key overlap 后安全过期 | 未执行 | `SKIPPED` |
| restore | count/hash/index/version/cursor 一致 | 未执行 | `SKIPPED` |
| RPO | ≤ 15 分钟 | 未测 | `UNVERIFIED` |
| RTO | ≤ 60 分钟 | 未测 | `UNVERIFIED` |
| capacity | 10k history、50/200 msg/s、100 pending | 未执行 | `SKIPPED` |
| rollback/fallback | old/new durable rollback，无双写 | 未执行 | `SKIPPED` |

## 风险、owner 与回滚

当前阻塞 owner：发布/平台 owner，负责提供隔离 topology、凭据和 fault/load harness。任何跨 scope、sealed-data、重复 terminal side effect、DLQ 无法安全重放或恢复 hash 不一致都必须停止 publisher，保留旧 artifact/worker/fallback，按 [Batch 4 runbook](phase-4-batch4-runbook.md) 回滚。

## 签字

| 角色 | 姓名 | 结论 | 时间 |
| --- | --- | --- | --- |
| Platform owner | 待定 | `PENDING` | — |
| Approver owner | 待定 | `PENDING` | — |
| Fluxion owner | 待定 | `PENDING` | — |
| Bids owner | 待定 | `PENDING` | — |

当前 P4-503 状态为 `SKIPPED`，仅在 P4-501 全部 live PASS 且 P4-502 完整观察窗口结束后生成最终签字报告。
