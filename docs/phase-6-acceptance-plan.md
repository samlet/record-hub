# Phase 6 验收计划

## Gates

| Gate | 内容 | 必需证据 | 失败动作 |
| --- | --- | --- | --- |
| P6-G1 Schema/Table | schema.org 语义、继承、tags、relations、views | schema manifest、兼容测试、scope/redaction/cost evidence | 禁止发布新 schema/table |
| P6-G2 Connector | Approver/Fluxion/Bids/Settlement connector | registry、fixture、SDK、owner side effect、late/retry/replay | 禁用 connector，保留 fallback |
| P6-G3 Workflow | Temporal/Conductor binding | history 引用/hash、timeout/retry、receipt、reconciliation | 停止 workflow adapter 新流量 |
| P6-G4 Event | NATS event/replay/table mapping | subject ACL、durable、DLQ、backpressure、gap/rebuild | 停止 publisher，进入 drain |
| P6-G5 Security | identity、scope、PII/sealed-data | rotation/revoke、cross-scope、safe log/DLQ/evidence scan | 旋转凭据并回滚 |
| P6-G6 Operations | SLO、cost、restore、runbook | dashboard/export、RPO/RTO、audit、rollback drill | 保持旧路径，不扩大灰度 |
| P6-G7 Release | tenant canary、compatibility、signoff | immutable manifest、observation window、risk owner、签字 | stop/disable/reconcile |

## 必须满足

1. P4-501、P5-G1～G7 全部 live `PASS`，不得继承旧的 `SKIPPED/PARTIAL`。
2. 每个真实 connector 至少完成一次 duplicate、late result、owner restart、result relay outage、DLQ 和 rollback 证明。
3. 每个 schema/table release 都有版本、迁移、兼容窗口、source pointer、redaction 和恢复 evidence。
4. workflow history 不包含完整业务 payload；Record Hub 不直接写 Approver、Fluxion、Bids 或 Settlement 事实。
5. 任何多地域决定必须由 P6-602 的实际容量、RPO/RTO、成本和运维证据支持，不能作为默认架构承诺。

## 当前判定

Phase 6 当前为 `PROPOSED`。P4/P5 live blocker 未清除前，所有 P6 batch 只能进行设计和静态 contract 工作。
