# Phase 6 Batch 1：P6-103 views and safe export

P6-103 固化 bounded view 的过滤/排序语法、签名 cursor、分页上限、查询成本预算、租户/工作区
限流和 backpressure。export 只能读取已发布 schema 与 view 的字段，敏感字段必须丢弃并写入审计。

机器可读 contract 位于 [`p6-103-view-contract-spec.json`](../deploy/local/p6/p6-103-view-contract-spec.json)，
验收结果位于 [`phase-6-views-contract.json`](phase-6-views-contract.json)。入口是 `make p6-103`。

这是静态 contract gate：`liveTraffic=false`，不执行查询、不创建索引、不导出记录，也不解除
P6-000 的 Phase 5 前置条件。
