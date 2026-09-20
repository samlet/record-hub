# Phase 6 workflow native evidence collector

P3-402 的隔离四 owner topology 已能真实启动 Fluxion Temporal worker、Bids Conductor worker 和
Record Hub binding API。为补齐 P6-400/P6-401 的 live evidence，新增 collector：

```text
make p6-workflow-evidence
```

collector 会在 `build/evidence/phase6/workflow-live-<UTC>/` 创建临时证据，执行：

- Fluxion Temporal diagnostic workflow 的新 execution replay：同一 typed input 生成相同
  operation ID，断言 snapshot ID/hash 相同、`replayed=true`、Mongo 只存在一条 binding snapshot；
- Bids Conductor diagnostic workflow 的重复 execution：同一 operation ID 断言 snapshot ID/hash
  相同、`replayed=true`；
- 两份 history/result 均扫描 `P3-WORKFLOW-SECRET-MARKER`，并保留 topology 四仓库 commit manifest。

collector 输出 `p6-workflow-evidence.json`，状态为 `PASS_NATIVE_EVIDENCE`。它不会自动改写
`phase-6-temporal-binding.json`、`phase-6-conductor-binding.json` 或任务表，因为 Phase 6 仍受
独立的 P4/P5 GA 前置门禁约束；该 native evidence 只证明 workflow binding/replay 本身已具备验收
材料。解除前置阻塞后，可将报告路径分别传给 `RECORD_HUB_P6_TEMPORAL_LIVE_REPORT` 和
`RECORD_HUB_P6_CONDUCTOR_LIVE_REPORT`（collector 同时生成 `p6-400-live-report.json` 和
`p6-401-live-report.json`），再重新运行对应 P6 gate。
