# P5-402 Production GA report

P5-402 建立受控的 Production GA 报告模板 `deploy/local/p5/p5-402-ga-report-template.json`。模板固定 RC manifest、四仓库 commit/artifact、实际 topology、P5-G1～G7、容量、RPO/RTO、retention、风险 owner、rollback 和五方签字字段；初始状态明确为 `PENDING`，决策为 `DO_NOT_START_PRODUCTION_GA`，不会把静态 contract 或 Phase 4 RC 当作 GA 证据。

静态入口：`make p5-402`。它验证模板 schema、P4-500 RC manifest 的六个 artifact、P4-501 live prerequisite、GA 报告字段和四仓库 commit boundary，并生成 `build/evidence/phase5/p5-402-*/p5-402.json`。

当前状态为 `PARTIAL`：报告模板和校验门已 PASS；P4-501 仍为 `SKIPPED`，P5-G1～G7、canary、capacity、RPO/RTO、正式 risk review 和 signoff 尚未完成，live 为 `SKIPPED`。不得修改模板为 PASS 或启动生产流量。

解除条件：完成 P4-501 全部 live gate、P5-G1～G7 evidence、immutable RC/artifact、P5-401 观察窗口、风险 owner review 和正式签字后，才允许生成独立的最终报告；任一 gate 为 `SKIPPED`、`PARTIAL` 或 `UNVERIFIED` 都必须保持 pre-GA。
