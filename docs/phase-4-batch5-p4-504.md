# P4-504 Fallback / 旧版本去留评审

## 决策

**KEEP：Beta live gate 完成前保留旧 worker、旧 schema reader 和 Fluxion local human-task fallback。** 不删除旧 artifact，不关闭兼容 reader，不撤销 fallback flag；旧路径保持可回滚但默认不承接新流量，除非按 incident runbook 启用。

## 理由

P4-501 的 P4-G1～G5 尚未在隔离拓扑中通过，P4-502 观察窗口和 P4-503 签字报告也尚未完成。当前删除会把不可验证的恢复、rotation、late-result 和 fallback 风险变成不可逆操作，违反 Phase 4 的禁止项。

## 复审条件

只有同时满足以下条件才允许重新评审删除窗口：

1. P4-G1～G5 全部 live `PASS`，无 `SKIPPED`/`PARTIAL`；
2. 至少一个完整 retention/retry/reconciliation 观察窗口结束，SLO、finding、sealed-data scan 均可接受；
3. Beta 报告已记录四仓库 commit/artifact、RPO/RTO、容量、风险 owner、rollback 和签字；
4. 已完成一次旧路径回滚演练，并确认 no-double-write、旧 schema reader 和 local fallback 可恢复。

该决策仅覆盖 Phase 4 Beta，不代表生产 GA 或正式合规批准；Phase 5 必须单独评审。
