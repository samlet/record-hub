# Phase 5 Production GA 验收计划

## Gates

| Gate | 内容 | 必需证据 | 失败动作 |
| --- | --- | --- | --- |
| P5-G1 Identity | OIDC/Dex、service principal、rotation、scope | token matrix、overlap/drain/revoke、audit | 停止发布，撤销新凭据并回滚 |
| P5-G2 Isolation | tenant/org/workspace/role negative matrix | Viewer/Editor/Operator/Admin + four owner cross-scope matrix | 立即阻止 GA |
| P5-G3 Data/Event HA | Mongo/NATS、Inbox/Outbox、replay/DLQ | failover、restore、hash/count/index/cursor、duplicate proof | 冻结 publisher，保留旧路径 |
| P5-G4 Recovery/SLO | capacity、lag/backlog/dead/finding、RPO/RTO | mixed load、restore drill、dashboard/export、签字 | 标记 UNVERIFIED，不得宣称达标 |
| P5-G5 Connector | Fluxion/Bids/Settlement contracts and owner side effects | schema/fixture/hash、version conflict、late result、Apply evidence | 禁用 connector，不能 fallback 到其他 owner |
| P5-G6 Security | secret/PII/sealed-data/supply-chain | Git/history/log/DLQ/metrics/evidence/artifact scans | 删除泄露 artifact，旋转凭据并升级 |
| P5-G7 Release | canary、observation、rollback、legacy review | RC manifest、window、rollback、GA report、签字 | 保留旧 worker/fallback，回到 pre-GA |

## GA 必须满足

1. P4-501 的 `SKIPPED` 不得继承为 Phase 5 PASS；所有 Phase 4 live hard gate 必须先清除。
2. P5-G1～P5-G7 全部 `PASS`，无 `SKIPPED`、`PARTIAL` 或 `UNVERIFIED`。
3. 结果必须包括四仓库实际 commit/artifact、实际 topology、容量、RPO/RTO、retention、风险 owner、rollback 和正式签字。
4. 旧 worker、旧 schema reader、Fluxion local fallback 和 rollback artifact 的去留必须有独立决策；未批准继续保留。

## 当前状态

Phase 5 仍为 Proposed；本文件只冻结方案和验收边界，不宣称生产 GA，也不启动生产流量。
