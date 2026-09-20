# Phase 6 event-to-table native evidence collector

P6-403 的原生证据入口为：

```text
make p6-event-table-evidence
```

collector 使用本机安装的 MongoDB replica set 与 NATS JetStream，依次验证：

- published mapping 的 JetStream restart/recovery；
- Mongo rebuild CAS、receipt、失败/取消保护；
- staged replay、checkpoint 补齐、read pointer 原子切换和 live continuation；
- mapping generation 的精确解析、撤销切换、last-known-good 保留；
- gap、same-version conflict 和 Inbox payload conflict 的拒绝/发现路径。

证据写入 `build/evidence/phase6/event-table-live-<UTC>/`，生成 `p6-403-evidence.json` 与供 gate
使用的 `p6-403-live-report.json`。collector 不改写 [`phase-6-event-table.json`](phase-6-event-table.json)，
因为 Phase 6 仍受独立 P4/P5 GA 前置门禁约束。
