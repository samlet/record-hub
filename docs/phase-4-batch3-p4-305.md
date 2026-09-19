# P4-305：Bids Approval Pilot fault/security matrix

- 状态：`PARTIAL`
- 入口：`make p4-batch3`
- 规范：[`p4-batch3-spec.json`](../deploy/local/p4/p4-batch3-spec.json)

六个静态 case（contract allowlist、migration、relay/materializer、result authority、Tender association、跨仓库 commit）全部 PASS。duplicate/restart/timeout/version/cross-org/sealed-data 的真实服务矩阵需要显式隔离的 Bids、Approver、Record Hub、MongoDB、PostgreSQL、NATS、Conductor、Temporal 拓扑，当前统一标记 `SKIPPED`，不会把普通本机服务误报为 live PASS。
