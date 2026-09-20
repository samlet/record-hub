# Phase 6 Batch 1：P6-100 schema.org registry

P6-100 固化了多维表格的语义类型、显式继承、字段类型和兼容策略。registry 只允许
`https://schema.org/` 下的显式 IRI；未知类型或属性进入 `QUARANTINE`，不会被推断为可用
字段。继承图必须有唯一 root 且无环，删除、重命名、类型收窄和新增必填字段都要求新版本。

机器可读 contract 位于 [`p6-100-schema-registry-spec.json`](../deploy/local/p6/p6-100-schema-registry-spec.json)，
验收结果位于 [`phase-6-schema-registry.json`](phase-6-schema-registry.json)。入口是 `make p6-100`。

这是静态 contract gate：`liveTraffic=false`，不启用 connector，不发布 NATS 消息，也不解除
P6-000 的 Phase 5 前置条件。
