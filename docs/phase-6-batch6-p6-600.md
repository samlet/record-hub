# Phase 6 Batch 6：P6-600 production-like mixed load

P6-600 固化四 owner mixed-load 评估：Record Hub、Approver、Fluxion、Bids 的 records/connector/
Temporal/Conductor/NATS/Mongo 链路按事件率阶梯执行，记录 latency、drain、retry/dead、query cost、
CPU/memory 和背压行为。每个样本必须隔离 tenant/workspace，raw payload 不进入 metrics/evidence；
超过 query/rate/connector 边界要拒绝或停止新流量。

机器可读 contract 位于 [`p6-600-mixed-load-spec.json`](../deploy/local/p6/p6-600-mixed-load-spec.json)，
验收结果位于 [`phase-6-mixed-load.json`](phase-6-mixed-load.json)。入口是 `make p6-600`。

安全计划预检入口是 `make p6-600-dry-run`，输出到被忽略的
`build/evidence/phase6/p6-600-dry-run.json`。它验证四 owner/六 plane、事件率阶梯、样本上限、
测量集合、SLO、背压和 cost label 边界，但不会启动负载、发布事件、改变限流或读取 live capacity
evidence。

已有 P3 synthetic capacity harness、query budget、rate/backpressure、projection metrics 和 NATS
bounded retry 基础；但四 owner 混合拓扑的真实容量、成本和 SLO evidence 尚未提供，当前为
`BLOCKED_BY_LIVE_CAPACITY_EVIDENCE`，不能据此宣称 production capacity。
