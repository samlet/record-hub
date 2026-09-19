# Phase 4 Integration Beta 需求规格

- 日期：2026-09-20
- 状态：Draft for review
- 技术方案：[phase-4-design.md](phase-4-design.md)
- 验收计划：[phase-4-acceptance-plan.md](phase-4-acceptance-plan.md)
- 任务分解：[phase-4-task-breakdown.md](phase-4-task-breakdown.md)

本文件中的 Phase 4 指 Record Hub 跨仓库集成计划，不等同于 Approver、Fluxion 或 Bids 各自内部的
同名阶段。

## 1. 目标与完成标准

Phase 4 要把已验证的技术链路提升为受限租户可运行、可恢复、可回滚的 Integration Beta。完成必须
同时满足：

- Phase 3 的 credential rotation、backup/restore、mixed capacity、upgrade/rollback 必选项有真实
  `PASS` 证据；
- Fluxion → Approver 低置信度派单审批具备灰度、五类终态、晚到结果、对账和 fallback；
- Bids → Approver 招标准备发布审批完成 request/result/Conductor 闭环，且高敏字段负向测试通过；
- Record Hub 审批关联视图只保存安全摘要，不执行审批决定或 owner workflow 动作；
- tenant、identity、NATS subject、数据库和日志证据边界均 fail closed；
- 四仓库 mixed workload、备份恢复、凭据轮换、rolling rollback 和 Beta 报告全部通过。

任何必选 live 项为 `SKIPPED`、`PARTIAL` 或存在未解释 `FAIL` 时，不得宣布 Phase 4 Beta 完成。

## 2. 范围与延期

### 2.1 必须交付

- 四仓库 release/evidence manifest、artifact checksum 和可保留的 evidence directory；
- reloadable workload issuer/JWKS 与每调用方向独立 client secret rotation；
- MongoDB、Approver/Fluxion/Bids PostgreSQL、JetStream 的隔离备份恢复；
- 四 owner mixed capacity、backlog drain、SLO 和资源基线；
- old/new artifact rolling upgrade/rollback 与 expand/contract migration；
- Fluxion Approval Beta、Bids Approval Pilot 和 Record Hub approval association projection；
- reconciliation、operator retry/replay、runbook、指标和最终 Beta 验收报告。

### 2.2 明确延期

- Bids 定标、报价、密封投标、合同签署、付款等高敏 command/approval；
- Record Hub 直接完成 Approver task、Temporal update 或 Conductor task；
- 多地域 active-active、自动灾备切换、零 RPO 或生产 GA 承诺；
- 任意用户脚本、任意 NATS subject、任意表格字段自动写回 owner；
- 删除旧 worker、旧 schema reader 或 Fluxion local fallback；该决定只能在 P4-G5 后单独评审。

## 3. 基线收口需求（P4-BAS）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-BAS-001 | 四仓库基线 | manifest 固定 commit、artifact checksum、构建命令、migration version、contract version |
| P4-BAS-002 | Phase 3 gap 映射 | P3-405/406/407/408、P3-308/110 均有 Phase 4 task、owner 和 live case |
| P4-BAS-003 | 可保留证据 | evidence 默认写入 `build/evidence/phase4/<run-id>` 或显式目录，生成 SHA-256 manifest 并供 CI 上传 |
| P4-BAS-004 | 契约镜像 | command/result、dispatch approval 与新增 association schema 在四仓库所需镜像逐文件一致 |
| P4-BAS-005 | 兼容规则 | v1 additive-only；breaking change 使用新版本、subject/endpoint、consumer 和迁移窗口 |
| P4-BAS-006 | 真依赖门禁 | fake/in-memory/direct DB mutation 不能替代 live gate；缺依赖必须记录 `SKIPPED` 与重试条件 |

## 4. 身份与安全需求（P4-SEC）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-SEC-001 | Human OIDC | Dex issuer/audience/tenant claim/mapping fail closed；四 Web client 独立 |
| P4-SEC-002 | Workload issuer | 真实 client-credentials token 含短 TTL、精确 audience/scope、tenant binding；不能用用户 token |
| P4-SEC-003 | JWKS overlap | 新旧 signing key 有界重叠；cache refresh 期间新旧 token 可按窗口验证，撤销后旧 key fail closed |
| P4-SEC-004 | Client secret rotation | 每调用方向独立双 secret overlap/reload；轮换不丢请求、不重复领域副作用 |
| P4-SEC-005 | NATS/DB credential | OIDC、NATS account/NKey、Mongo/PostgreSQL credential 分离；subject/database 最小授权 |
| P4-SEC-006 | 租户隔离 | tenant/workspace/organization 由服务端映射；cross-tenant API、message、association 全部拒绝且不泄露存在性 |
| P4-SEC-007 | Bids 数据最小化 | schema、payload、history、log、DLQ、metrics、evidence 不含报价、投标正文、密封文件、评分、合同、付款数据 |
| P4-SEC-008 | 安全扫描 | token、cookie、secret、PII、文件 URL 和高敏 Bids fixture 扫描无发现；测试 secret 不进入 Git |

## 5. Fluxion Approval Beta 需求（P4-FLX）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-FLX-001 | Tenant 灰度 | feature flag 默认关闭，精确到 tenant/workspace；不允许全局误开 |
| P4-FLX-002 | Stable request | external request ID、dispatch generation、proposal hash、workflow/run ref 和 requester identity 不变 |
| P4-FLX-003 | Transaction outbox | Activity 只写 request/outbox；响应丢失使用原 ID 查询，不在 deterministic workflow 中发 HTTP |
| P4-FLX-004 | 五类终态 | approved、rejected、withdrawn/cancelled、expired、failed 均有 live 路径和稳定错误语义 |
| P4-FLX-005 | 晚到结果 | generation/project 状态已变化时不覆盖新事实，写 reconciliation finding |
| P4-FLX-006 | 在途 drain | 关闭 flag 后停止新 request；既有 request 继续管理、drain 或显式取消，不遗弃 |
| P4-FLX-007 | Local fallback | Approver 不可用时按 policy 等待、过期或转 local human task；单 generation 不双写 |
| P4-FLX-008 | Replay safety | Temporal replay/Continue-As-New 携带稳定 request ID，不重新创建 Application 或重复 update |

## 6. Bids Approval Pilot 需求（P4-BID）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-BID-001 | 受限业务切片 | 仅“招标准备发布审批”；定标、合同、付款和密封数据不在 policy/schema 中 |
| P4-BID-002 | Request allowlist | 仅稳定 tender/lot ref、安全显示名、计划发布时间、组织 ref、schema/version/hash |
| P4-BID-003 | Request Outbox | Bids 在业务事务写 request/outbox；relay 使用 `bids-to-approver` workload identity |
| P4-BID-004 | Result Inbox | external request ID + decision version/hash 去重；不同 hash/终态形成冲突 finding |
| P4-BID-005 | 原子完成 | Result Inbox、Bids 状态和 Conductor task-completion Outbox 同一 PostgreSQL 事务提交 |
| P4-BID-006 | Conductor retry | task retry 复用原 request ID；worker crash/ACK loss 不重复 Application 或完成 task |
| P4-BID-007 | Version guard | Bids 应用决定前在行锁内比较 tender generation/version；过期结果不改变当前事实 |
| P4-BID-008 | Negative matrix | 高敏字段、cross-org、未知 schema、oversize、unknown terminal、late result 全部 fail closed |
| P4-BID-009 | 单一审批 authority | organization/generation feature flag 在 external Approver 与本地 HUMAN task 中二选一；关闭后在途外部申请可 drain/cancel |

## 7. Record Hub 关联与对账需求（P4-RH）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-RH-001 | Typed association | 关联 source resource、Approver Application 和 workflow instance，包含 tenant、version 和 stable refs |
| P4-RH-002 | 安全投影 | 仅状态、version、hash、syncedAt、lag 和 finding；不保存审批正文、附件或 owner payload |
| P4-RH-003 | 查询与授权 | 按 tenant/workspace、owner、状态、时间查询；Viewer/Editor/Operator 权限分离 |
| P4-RH-004 | Source monotonicity | duplicate 忽略、gap 保留并告警、旧 version 不覆盖新投影、冲突进入 finding |
| P4-RH-005 | Reconciliation taxonomy | 至少支持 MISSING_REQUEST/RESULT、TERMINAL_CONFLICT、VERSION_DRIFT、WORKFLOW_NOT_UPDATED |
| P4-RH-006 | Operator recovery | retry/replay 需要 operator、原因和 Idempotency-Key；禁止直接修改业务终态 |
| P4-RH-007 | Deep link | “去审批”仅跳转 Approver 授权页面；Record Hub 不代理审批决定 |

## 8. 恢复、容量与发布需求（P4-OPS）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P4-OPS-001 | Recovery set | 同一 manifest 关联 Mongo、三 owner PostgreSQL、JetStream backup time/checksum/version |
| P4-OPS-002 | Restore verification | 隔离恢复后 count/hash/index/version、Inbox/Outbox、receipt、cursor 与未完成 workflow 可对账 |
| P4-OPS-003 | RPO/RTO | 暂定 RPO ≤15 分钟、RTO ≤60 分钟；必须由 live exercise 证明，否则 `UNVERIFIED` |
| P4-OPS-004 | Mixed capacity | 每 owner 10,000 历史、steady 50 msg/s、burst 200 msg/s、100 并发审批，记录硬件和并发 |
| P4-OPS-005 | SLO | 暂定 projection p95≤5s/p99≤30s、无故障 command terminal p95≤5s；报告实际值与偏差 |
| P4-OPS-006 | Backlog recovery | 停 consumer 10 分钟后测 drain time、重复副作用、storage 增长和 oldest age |
| P4-OPS-007 | Rolling rollback | immutable old/new artifacts、expand/contract、durable consumer、feature flag 和旧 worker 窗口 live PASS |
| P4-OPS-008 | Observability | command/approval/projection/backlog/dead/finding/recovery 有有限基数指标和 owner/runbook 告警 |
| P4-OPS-009 | Evidence integrity | manifest、redacted config、results、bounded logs、metrics、DB assertions 有 hash 且进入 CI artifact |
| P4-OPS-010 | Beta report | 列出四仓库 commit/artifact、全部 case、RPO/RTO、容量、风险 owner、rollback 和签字结论 |

## 9. 非功能约束

- command/result 最大 256 KiB；approval connector 使用更小的固定 allowlist 和 size limit。
- 同步 HTTP timeout 不超过 10 秒；Activity/relay retry 必须有最大次数、总时长和 jittered backoff。
- Inbox/Outbox 使用有界 batch、lease 和 shutdown drain；禁止无界 goroutine/thread/queue。
- metrics label 不能使用 tenant、resource、operation、request 或 workflow ID。
- 本地单节点可验证功能，但不能证明 HA；容量和 RPO/RTO 报告必须记录拓扑限制。
- `SKIPPED` 不计入 Beta Gate 通过，缩小规模必须记录偏差且不能外推。

## 10. 发布门

| Gate | 进入条件 | 退出条件 |
| --- | --- | --- |
| P4-G0 Contract | 方案、需求、任务和验收计划评审通过 | manifest、mirror、compatibility、evidence schema 全部 PASS |
| P4-G1 Safety | G0 通过、feature flags 默认关闭 | tenant/identity/ACL/field negative matrix 全部 PASS |
| P4-G2 Reliability | G1 通过、两个 approval slice 可运行 | duplicate/loss/restart/late-result/reconciliation 全部 PASS |
| P4-G3 Recovery | 可冻结并恢复隔离 targets | restore assertions 与 RPO/RTO live PASS |
| P4-G4 Capacity | G2/G3 通过、规模和硬件固定 | mixed load、SLO、backlog drain 有可接受结论 |
| P4-G5 Release | old/new artifacts 与 rollback runner 就绪 | rotation、rolling rollback、fallback 和最终 Beta 报告全部 PASS |

Gate 必须按顺序关闭。P4-G5 前不得删除兼容 reader、旧 worker 或本地审批 fallback。
