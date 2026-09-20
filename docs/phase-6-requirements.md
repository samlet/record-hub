# Record Hub Phase 6 需求基线

状态：Proposed。所有 live 需求必须在 Phase 5 GA 证据完成后，在隔离 topology 中重复验证；设计阶段不得
把 `SKIPPED`、`PARTIAL` 或 `UNVERIFIED` 继承为 PASS。

## P6-DATA：多维表格与 schema

- **P6-DATA-001 MUST**：Table、Field、Record、Tag、Relation、View 全部具备 tenant/workspace scope、版本和审计。
- **P6-DATA-002 MUST**：字段类型、JSON Schema 约束、schema.org IRI、继承和兼容窗口可查询；未知继承 fail closed。
- **P6-DATA-003 MUST**：tags 使用受控字典，不得替代权限、owner 事实或审批终态；tag 变更可追踪。
- **P6-DATA-004 MUST**：跨系统 relation 只允许 typed ref、安全摘要、source version 和 snapshot hash，禁止敏感正文复制。
- **P6-DATA-005 MUST**：view、导出和查询都执行 scope、redaction、pagination、排序和 cost bound。

## P6-INT：真实业务 connector

- **P6-INT-001 MUST**：connector registry 以 `(owner, event/action, schemaVersion)` 精确匹配，未知 key fail closed。
- **P6-INT-002 MUST**：Approver、Fluxion、Bids 各自使用独立 adapter、workload identity、Inbox/Outbox 和 reconciliation。
- **P6-INT-003 MUST**：connector 支持 enable/disable、兼容窗口、在途 drain、重放、晚到结果和 rollback。
- **P6-INT-004 MUST**：Settlement 只允许安全摘要/关联/receipt；Apply 与财务事实继续由 Approver/Settlement 拥有。
- **P6-INT-005 MUST**：新 connector 需要 contract、fixture、migration、redaction 和 live fault matrix，不能以 mock owner 代替。

## P6-WF：Temporal/Conductor binding

- **P6-WF-001 MUST**：workflow history 只保存 typed ref、schema version、record version、snapshot hash 和 operationId。
- **P6-WF-002 MUST**：Activity/worker 具备 timeout、retry、idempotency、expected version、receipt polling 和 reconciliation。
- **P6-WF-003 MUST**：READ_SNAPSHOT、READ_LATEST、COMMAND_RECEIPT、OWNER_APPLY_REQUIRED 的事务属性可审计。
- **P6-WF-004 MUST**：不得导入其他 workflow 系统内部包，不得由 Record Hub 直接完成 Temporal/Conductor 业务 task。

## P6-OPS：事件与运营

- **P6-OPS-001 MUST**：每个 connector、schema family 和表格引用有 metrics、lag、backlog、retry、dead、finding 和 recovery 信号。
- **P6-OPS-002 MUST**：NATS subject、durable、retention、DLQ 和 replay 权限按 registry/tenant 固定，不开放任意 publish。
- **P6-OPS-003 MUST**：导出、replay、projection rebuild、connector disable 和 rollback 都记录 operator audit。
- **P6-OPS-004 MUST**：每项扩展有成本上限、分页、速率限制和背压；超限 fail closed，不降级为全量扫描。

## P6-SEC：身份和数据安全

- **P6-SEC-001 MUST**：Dex/OIDC 用户 token 校验 issuer、audience、signature、expiry、subject 和 membership。
- **P6-SEC-002 MUST**：owner workload principal 独立、最小 scope，支持 overlap、drain、revoke 和审计。
- **P6-SEC-003 MUST**：PII、sealed bid、报价、金额、银行/发票附件和完整 workflow snapshot 不进入日志、指标、DLQ、evidence 或安全投影。

## P6-REL：发布治理

- **P6-REL-001 MUST**：每个 connector/schema release 固定 commit、artifact、contract/migration/config digest 和兼容矩阵。
- **P6-REL-002 MUST**：按 tenant/workspace 灰度，完整 observation window 后才扩大；保留 old worker、fallback 和 rollback artifact。
- **P6-REL-003 MUST**：任何破坏性 schema、scope 泄漏、重复副作用或 redaction 失败立即停止新流量并回滚。
