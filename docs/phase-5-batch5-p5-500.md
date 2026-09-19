# P5-500 GA candidate manifest preflight

P5-500 目前只实现 fail-closed preflight，不创建 GA candidate manifest。候选版本必须同时拥有 P4-501 全部 live gate、P5-G1～G7 全部 `PASS`、四仓库 immutable artifact/commit digest、contract/migration/config digest、实际 topology、capacity、RPO/RTO、retention、rollback、risk owner 和正式签字；`SKIPPED`、`PARTIAL`、`UNVERIFIED` 或 `PENDING` 不得继承。

静态入口：`make p5-500`。它验证 mandatory gate contract、当前没有 candidate manifest、Phase 4 RC 仍含 live `SKIPPED`，并生成 `build/evidence/phase5/p5-500-*/p5-500.json`。当前报告状态为 `BLOCKED`，决策为 `DO_NOT_CREATE_GA_CANDIDATE`；该入口在前置未满足时不会写入 candidate 文件。

解除条件：先完成 P4-501 和 P5-G1～G7 的独立 live 证据、P5-401 观察窗口与 P5-402 正式签字，再由发布 owner 使用 immutable artifact root 运行候选组装器。当前不能创建 GA candidate，也不能进入 P5-501 rollout。
