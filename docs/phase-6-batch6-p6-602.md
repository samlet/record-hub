# Phase 6 Batch 6：P6-602 multi-region decision record

P6-602 固化 multi-region 的决策边界：默认仍是 single-region HA；active-passive/active-active 只能
在 mixed-load、Mongo replication/PITR、NATS replay、Temporal/Conductor owner failover、RPO/RTO、
成本、runbook、安全矩阵和 risk-owner signoff 证据齐全后评估。未实测不得承诺零 RPO、自动跨地域
补偿或 XA/2PC。

机器可读 contract 位于 [`p6-602-multi-region-spec.json`](../deploy/local/p6/p6-602-multi-region-spec.json)，
验收结果位于 [`phase-6-multi-region.json`](phase-6-multi-region.json)。入口是 `make p6-602`。

现有 Phase 5/6 文档已明确 single-region 默认、RPO/RTO 目标和 owner/data-plane 边界；但实际多地域
容量、故障切换、成本和签字 evidence 尚未提供，当前为 `BLOCKED_BY_LIVE_MULTI_REGION_EVIDENCE`，
go/no-go 保持 `NO_GO`。
