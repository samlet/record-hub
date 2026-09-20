# Phase 6 Batch 1：P6-101 controlled tags

P6-101 固化受控 tag dictionary、tenant/workspace/table scope、互斥组和不可变审计边界。
未知 tag、跨租户赋值和带有权限/密钥含义的 tag 都进入 `QUARANTINE`；tags 只用于筛选、展示
和流程速查，不能替代 RBAC、认证或业务事实状态。

机器可读 contract 位于 [`p6-101-tag-contract-spec.json`](../deploy/local/p6/p6-101-tag-contract-spec.json)，
验收结果位于 [`phase-6-tags-contract.json`](phase-6-tags-contract.json)。入口是 `make p6-101`。

这是静态 contract gate：`liveTraffic=false`，不改变现有 record API，不发布 NATS 消息，也不解除
P6-000 的 Phase 5 前置条件。
