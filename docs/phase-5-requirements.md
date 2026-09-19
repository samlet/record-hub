# Record Hub Phase 5 需求基线

状态：Proposed。所有 `MUST` 需求都需要可重复证据；没有隔离拓扑或生产级凭据时只能标记 `UNVERIFIED/SKIPPED`。

## P5-PLAT：平台与多租户

- **P5-PLAT-001 MUST**：tenant、organization、workspace、membership、service principal、connector mapping 具备 create/enable/disable/rotate/revoke 生命周期和审计。
- **P5-PLAT-002 MUST**：四 owner 的 tenant/org/workspace/resource scope 交叉访问全部拒绝，且不泄露资源存在性。
- **P5-PLAT-003 MUST**：Viewer、Editor、Operator、Admin 权限在 records/schema/association/operations/command/console 一致；审批终态无 Record Hub 直接改写入口。
- **P5-PLAT-004 MUST**：API、JetStream、Mongo projection 和 worker 的 contract version、migration version、feature flag 均可查询并可回滚。

## P5-SEC：身份与安全

- **P5-SEC-001 MUST**：Dex/OIDC 用户认证校验 issuer、audience、signature、expiry、subject 和 membership；生产 secret 不使用本地明文文件。
- **P5-SEC-002 MUST**：每个 workflow owner 的机器身份独立，最小 scope，支持双 key/certificate overlap、in-flight drain、撤销和审计。
- **P5-SEC-003 MUST**：private key、token、PII、sealed bid、报价、金额、银行/发票附件和完整 workflow snapshot 不进入 logs、metrics、DLQ、evidence 或安全 projection。
- **P5-SEC-004 MUST**：HTTP、NATS、Mongo、backup、operator API 的访问均有认证、授权、TLS/网络边界和 bounded audit。

## P5-DATA：数据与事件

- **P5-DATA-001 MUST**：Mongo replica set、index、retention、archive、backup/PITR 和 restore assertion 有版本化 manifest。
- **P5-DATA-002 MUST**：JetStream stream/consumer subject、durable、ack、redelivery、DLQ、retention 和副本数固定并可验收。
- **P5-DATA-003 MUST**：Inbox/Outbox/receipt/finding 使用稳定 idempotency key、payload hash、lease 和 bounded retry；重复投递不重复 owner-side effect。
- **P5-DATA-004 MUST**：rebuild/replay/CAN/restart/late result/gap/conflict 均能证明 snapshot hash、source version 和 projection pointer 单调。

## P5-INT：接入与业务边界

- **P5-INT-001 MUST**：connector registry 按 `(connector, event, schemaVersion)` fail closed，禁止未知 connector fallback。
- **P5-INT-002 MUST**：Fluxion/Bids 现有 approval connector 保持兼容窗口、禁用、回滚和 reconciliation 能力。
- **P5-INT-003 MUST**：Settlement confirmation 只投影安全摘要和关联；Approver/Settlement 直接完成审批与 Apply，Record Hub 不持有财务权威。
- **P5-INT-004 MUST**：新接入必须提交 owner、scope、contract/fixture、migration、rollback、redaction 和 live fault matrix。

## P5-OPS：SLO、灾备与运维

- **P5-OPS-001 MUST**：terminal/materialization/delivery/lag/backlog/dead/finding/recovery 有 bounded metrics、dashboard 和告警。
- **P5-OPS-002 MUST**：单地域 HA、节点重启、Mongo/NATS 故障、restore、credential rotation 和 rolling rollback 有实测证据。
- **P5-OPS-003 MUST**：Beta/GA 报告记录实际 topology、容量、RPO/RTO、风险 owner、rollback、retention 和签字；未实测不得声明达标。
- **P5-OPS-004 MUST**：runbook 定义 owner、触发、停止、恢复、回滚、重放、对账和升级路径，且 operator action 可审计。

## P5-REL：发布治理

- **P5-REL-001 MUST**：每个 RC 固定四仓库 commit、artifact SHA-256、contract/migration/config digest 和兼容矩阵。
- **P5-REL-002 MUST**：生产发布遵循 expand-contract、old/new overlap、单 tenant 灰度、完整观察窗口和可回滚 gate。
- **P5-REL-003 MUST**：旧 worker、schema reader、local fallback 和 rollback artifact 在未完成独立评审前不得删除。
- **P5-REL-004 MUST**：生产 GA 需要四 owner、平台、安审/运维责任人签字；P4 `SKIPPED` 不得被隐式继承为 GA PASS。
