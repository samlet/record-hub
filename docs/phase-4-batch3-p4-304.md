# P4-304：Tender↔Application typed association

- 状态：`PARTIAL`（静态 PASS，Mongo/NATS live SKIPPED）
- Record Hub commit：待本次提交

Record Hub 新增 Mongo 投影 `tender_application_associations` 与只读 `/api/v1/associations/tender-applications`。引用仅允许 `bids:TENDER:<uuid>` 和 `approver:APPLICATION:<id>`，沿用 tenant/workspace authorizer、monotonic sourceVersion、GAP/CONFLICT 状态；不会复制报价、文件或 workflow input。
