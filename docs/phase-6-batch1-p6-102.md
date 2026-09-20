# Phase 6 Batch 1：P6-102 typed relations

P6-102 固化跨表/跨 owner relation 的 typed ref、cardinality、source version、snapshot hash
和 reconciliation finding。安全摘要只允许稳定标识、状态、时间和 hash；workflow input、原始正文、
token、密钥、报价和银行信息一律禁止复制，违反时 `DROP_AND_AUDIT`。

机器可读 contract 位于 [`p6-102-relation-contract-spec.json`](../deploy/local/p6/p6-102-relation-contract-spec.json)，
验收结果位于 [`phase-6-relations-contract.json`](phase-6-relations-contract.json)。入口是 `make p6-102`。

这是静态 contract gate：`liveTraffic=false`，不解析新跨系统 relation，不发布 NATS 消息，也不解除
P6-000 的 Phase 5 前置条件。
