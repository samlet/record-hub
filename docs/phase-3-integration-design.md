# Phase 3 真实业务系统接入技术方案

- 日期：2026-09-18
- 状态：Proposed
- 上游基线：[Integration Beta 需求](phase-2-requirements.md)、[Controlled Command 设计](phase-2-command-design.md)
- 覆盖仓库：Record Hub、Approver、Fluxion、Bids

## 1. 结论

Phase 3 不再扩展新的平台能力面，目标是把已经完成的 Record Hub 数据投影、Workflow Binding、
Command Gateway 和 result receipt 接入真实业务进程，并形成可以重启、重投和审计的端到端闭环。

实施采用两个连续垂直切片：

1. **Fluxion Controlled Command**：以 `fluxion / PROJECT / project.annotate` 为首个低敏命令，
   打通 Record Hub receipt、JetStream command、Fluxion PostgreSQL Inbox/业务事务/result Outbox、
   Record Hub 终态回写。
2. **Fluxion → Approver 审批试点**：把低置信度派单审批接入 Approver 独立服务；正常履约仍由
   Fluxion Temporal Workflow 拥有，审批申请、人工决定和结果投递由 Approver 拥有。

第一条切片通过故障矩阵后，再把相同 owner adapter 模式扩展到 Approver 和 Bids。Bids 的定标、
投标、合同、付款等高敏动作不作为首批 command 或审批试点。

## 2. 当前基线

| 系统 | 已具备 | 本阶段缺口 |
| --- | --- | --- |
| Record Hub | 三系统摘要投影、Fluxion/Bids Binding、Command Gateway、operation receipt、`COMMAND_RESULTS` consumer、NATS 权限基线 | 共享 command/result schema、真实 owner E2E、全拓扑监督脚本、运维视图 |
| Approver | Temporal、通用 integration request/materialization/result Outbox、Connector SPI、Application 摘要 Outbox | Record Hub command Inbox、Fluxion connector/contract、Fluxion 结果 delivery/reconciliation |
| Fluxion | Temporal、PostgreSQL、Project 摘要 Outbox、Record Hub Binding Activity、现有低置信度人工审批 | durable command consumer、PostgreSQL command Inbox/result Outbox、Approver request/result adapter |
| Bids | Conductor、PostgreSQL/GORM、Tender 摘要 Outbox、Record Hub Binding task、通用业务 Outbox | durable command consumer、command Inbox/result Outbox、后续 Approver connector |

本方案盘点时的仓库基线为：Record Hub `a3a4849`、Approver `71e7f4f`、Fluxion `5f872de`、
Bids `21599a1`。它们只用于说明设计依据；正式验收必须在 evidence manifest 中记录当次实际 commit。

Record Hub 中的 Mongo `InboxService` 是协议参考和测试基线，不会作为三个 owner system 的业务 Inbox。
每个 owner 必须在自己的 PostgreSQL 事务内实现 Inbox、领域变更和 result Outbox，不能跨库调用
Record Hub MongoDB，也不能导入 Record Hub 内部 Go 包。

## 3. 总体架构

```text
Workflow/service --workload token--> Record Hub Command API
        |                                  |
        |                                  +-- Mongo operation receipt/audit
        |                                  +-- commands.<owner>.<action>.v1
        |                                                        |
        |                                                        v
        |                              owner durable pull consumer
        |                                                        |
        |                         PostgreSQL transaction          |
        |                   Inbox + domain fact + result Outbox   |
        |                                                        |
        |                    results.<owner>.<action>.v1 <--------+
        |                                  |
        +<-- poll receipt / safe event -----+-- Record Hub terminal CAS

Fluxion Temporal Activity/outbox --HTTPS--> Approver integration API
        |                                      |
        |                               immutable Application
        |                               Temporal approval flow
        |                                      |
        +<-- Fluxion result Inbox <--- Approver result Outbox/connector
                    |
                    +-- Temporal update outbox --> existing dispatch child

Approver/Fluxion/Bids domain Outbox --> NATS domain events --> Record Hub projections
```

边界：

- Record Hub 只路由命令、保存 receipt 和安全结果，不执行 owner 领域规则。
- NATS 至少一次投递；业务幂等依赖 owner Inbox，不依赖 JetStream deduplication window。
- Temporal/Conductor deterministic code 不直接访问 HTTP、NATS、MongoDB 或 PostgreSQL。
- Approver 以独立服务 API/Connector 接入，不作为 Fluxion/Bids child workflow，也不共享 workflow history。
- 业务数据库仍是最终事实；Record Hub 投影和审批关联均为派生视图。

## 4. 共享命令协议

### 4.1 Subject 与 consumer

| Stream | Subject | Durable consumer |
| --- | --- | --- |
| `APPROVAL_COMMANDS` | `commands.approver.>` | `approver-command-inbox-v1` |
| `OWNER_COMMANDS` | `commands.fluxion.>` | `fluxion-command-inbox-v1` |
| `OWNER_COMMANDS` | `commands.bids.>` | `bids-command-inbox-v1` |
| `COMMAND_RESULTS` | `results.<ownerSystem>.<action>.v1` | `record-hub-command-results-v1` |

owner consumer 只能使用自己的预创建 durable、ACK subject 和 `_INBOX`；禁止 core subscription
绕过 durable cursor。result publisher 只能发布自己的 `results.<system>.>`。

### 4.2 Command envelope

当前 v1 字段保持不变：

```json
{
  "operationId": "cmd-...",
  "tenantId": "tenant-1",
  "workspaceId": "workspace-1",
  "policyId": "project.annotate",
  "ownerSystem": "fluxion",
  "resourceType": "PROJECT",
  "action": "project.annotate",
  "purpose": "project-annotation",
  "resourceRef": "fluxion:PROJECT:<uuid>",
  "expectedVersion": 7,
  "payloadHash": "sha256:...",
  "payload": {},
  "requestedBy": {"issuer": "...", "subject": "..."},
  "createdAt": "2026-09-18T00:00:00Z"
}
```

JSON Schema、canonical fixture、错误码和 manifest 在四个仓库镜像。manifest 固定逐文件 SHA-256；
任何删除、改名、必填语义变化或枚举复用必须发布 v2，不能静默改变 v1。

### 4.3 Result envelope

```json
{
  "eventId": "uuid",
  "operationId": "cmd-...",
  "tenantId": "tenant-1",
  "workspaceId": "workspace-1",
  "ownerSystem": "fluxion",
  "action": "project.annotate",
  "status": "SUCCEEDED",
  "resultHash": "sha256:...",
  "resultVersion": 8,
  "occurredAt": "2026-09-18T00:00:01Z"
}
```

终态只允许 `SUCCEEDED/REJECTED/FAILED/EXPIRED`。错误只返回 allowlist `errorCode` 和不超过
512 字符的 `safeError`，不得携带 SQL、堆栈、token、PII、投标正文或文件 URL。同一 event ID
必须对应完全相同的终态内容；相同 operation ID 的不同终态视为冲突并进入人工排查。

## 5. Owner Inbox 与结果 Outbox

每个 owner 使用本语言和本数据库实现相同语义，最小数据模型为：

```text
record_hub_command_inbox
  operation_id                 UNIQUE
  tenant_id / workspace_id
  owner_system / action / resource_ref
  expected_version / payload_hash
  status                       CLAIMED|SUCCEEDED|REJECTED|FAILED
  result_event_id / result_hash / result_version
  safe_error_code
  created_at / updated_at

record_hub_command_result_outbox
  event_id                     UNIQUE
  operation_id                 UNIQUE
  subject / payload / payload_hash
  status                       PENDING|PROCESSING|SENT|DEAD
  attempts / available_at
  lease_owner / lease_expires_at
  last_error_code / sent_at / timestamps
```

单条 command 的处理顺序：

1. consumer 在解析前检查 256 KiB、UTF-8、subject 和 v1 schema；非法消息不进入业务事务。
2. 开启 owner PostgreSQL transaction，按 operation ID 插入或锁定 Inbox。
3. 已存在且 payload hash 相同：返回原结果；hash 不同：记录冲突，不执行领域副作用。
4. 锁定 owner aggregate，校验 tenant、resource type、当前版本和业务前置条件。
5. 执行领域变更，并在同一事务写审计、Inbox 终态和 result Outbox。
6. 提交成功后 ACK command；提交失败则 NAK，不发布成功结果。
7. 独立 relay 发布 result event；发布确认后将 Outbox 标为 `SENT`。
8. Record Hub result consumer 以 operation revision CAS 推进 receipt；重复结果只读返回原终态。

可重试数据库/网络错误不应提前生成 `FAILED` 终态；只有业务拒绝或确认不可恢复的处理错误才产生
终态结果。MaxDeliver 后的命令进入安全 DLQ，并由 operator 对照 owner Inbox 决定 replay，不能手改业务表。

## 6. 首个 Controlled Command：Fluxion Project Annotation

### 6.1 选择原因

`project.annotate` 只追加内部时间线说明，不改变派单、签到、验收、收款和补偿状态，不包含客户、
地址、金额或人员信息。它可以用一条新的 `VOID` annotation 进行语义补偿，同时保留完整审计链，
适合作为真实 owner transaction 的第一条切片。

允许 payload：

```json
{
  "annotationId": "uuid",
  "mode": "APPEND",
  "text": "不超过 500 字符的内部说明",
  "voidsAnnotationId": null
}
```

- `mode=APPEND` 创建 append-only `project_events(kind=integration)`。
- `mode=VOID` 必须引用同一项目中已存在且未 void 的 annotation，只追加作废事件，不删除历史。
- operation ID、annotation ID、payload hash 均唯一；重复投递不产生第二条时间线。
- `expectedVersion` 对应 Fluxion `projects.summary_version`。成功时版本 +1，并在同一事务写 Project
  summary Outbox 和 command result Outbox。
- 项目不存在、租户不匹配、版本过期、终态项目禁止写入等均返回稳定 `REJECTED` 错误码。

该命令先只对一个测试 tenant/workspace 开启精确 policy。通过完整故障矩阵前，不开放 reschedule、
reassign、cancel、change order、approval decision 或 payment 类 command。

## 7. Approver 审批试点：Fluxion 低置信度派单

### 7.1 所有权

- Fluxion 拥有 Project、候选人员、派单结果和 Temporal 履约状态。
- Approver 拥有 Application、Process、Task、人工决定和审批审计。
- Record Hub 只投影两侧安全摘要，并以 stable refs 建立关联，不代替任何一侧执行决定。

### 7.2 请求链路

Fluxion 低置信度派单 Activity 不直接在 Temporal Workflow 中发 HTTP。Activity 在 PostgreSQL 中
创建稳定 `external_approval_request` 和 request Outbox；relay 使用 `fluxion-to-approver` workload
identity 调用 Approver integration API。响应丢失时以同一 external request ID 查询 receipt。

Approver 增加 `FLUXION` connector contract handler，复用现有 integration request、materialization、
Temporal process、result Outbox、delivery SPI 和 reconciliation registry，不复制第二套 dispatcher。

建议稳定名称：

```text
request event: fluxion.dispatch-approval.requested@v1
Approver action: fluxion.dispatch.apply@v1
result event:  fluxion.dispatch-approval.result@v1
```

### 7.3 结果链路

Approver result handler 调用 Fluxion integration-only endpoint。Fluxion 在同一事务中写 result Inbox
和 Temporal update Outbox；dispatcher 再调用现有 dispatch child 的 `approveDecision` update。重复
decision version 不重复唤醒 Workflow，同版本不同 hash 报冲突。

Approver 当前通用结果枚举只有 `APPROVED/REJECTED/CANCELLED/FAILED`。本阶段需要以向后兼容方式增加
`WITHDRAWN/EXPIRED` 并同步 contract handler、旧 connector 和 reconciliation 测试；在该变更完成前，
不得用自由文本 reason 冒充缺失终态。

| Approver 终态 | Fluxion 行为 |
| --- | --- |
| `APPROVED` | 使用申请中固定的 proposal；重新校验 Project/dispatch generation 后回打 Temporal update |
| `REJECTED` | 走现有固定策略兜底派单 |
| `WITHDRAWN` / `CANCELLED` | 关闭外部申请，创建本地人工任务，不自动改变派单 |
| `EXPIRED` | 创建本地人工任务并告警，不自动采用过期 proposal |
| `FAILED` | 保留等待状态，进入有界重试/本地人工兜底 |

Apply 前必须比较 Project ID、dispatch generation、proposal hash 和 workflow run reference。过期审批
不得作用于已改派、已取消或已进入下一阶段的 Project。

### 7.4 回滚与灰度

- feature flag 按 tenant/workspace 开启；默认仍使用 Fluxion 现有本地 human task。
- 灰度时同一 dispatch generation 只能选择本地或 Approver 一条路径，禁止双写双审批。
- 关闭 flag 只影响新申请；已发出的 external request 必须完成、取消或由 operator 显式终止。
- Approver 不可用时 request Outbox 积压，Temporal 等待有界；达到业务超时后转本地人工任务。

## 8. Approver 与 Bids owner adapter 扩展

Fluxion 切片通过后，分别实现同构 adapter：

- Approver：`application.annotate`，只追加 Application integration note，不改变审批决定和任务状态。
- Bids：`tender.annotate`，只追加 Tender 内部审计说明，不触碰投标、报价、开标、定标、合同或付款。

两个 adapter 必须使用各自 PostgreSQL/Flyway 或 GORM migration、Inbox、result Outbox 和 NATS durable。
不能复制 Fluxion 表结构而忽略本系统 tenant/organization、aggregate version 和审计约束。

Bids → Approver 生命周期审批属于后续扩展。只有 Fluxion 审批试点的五种终态、晚到结果、重启和
回滚全部通过后，才评审 Tender publication 等业务节点；密封投标、定标、合同签署和付款继续排除。

## 9. 身份与权限

- human 登录继续使用本地 Dex Authorization Code + PKCE，各 Web client 独立。
- service-to-service 使用独立 workload issuer/client；不依赖当前稳定 Dex 的 `client_credentials`。
- 每个方向独立 principal：`fluxion-to-record-hub`、`record-hub-to-fluxion`、
  `fluxion-to-approver`、`approver-to-fluxion` 等，禁止共享超级 client。
- command submit policy 精确绑定 issuer、subject、audience、scope、tenant、workspace、purpose、
  owner、resource type 和 action。
- requester human identity 是业务字段，不得冒充 workload token subject。
- NATS credential 与 OIDC token 分离；MongoDB 只由 Record Hub 访问，owner 不持有 Mongo credential。

## 10. 可观测性和运维

统一关联键：

```text
traceId / operationId / externalRequestId / eventId / tenantId / workspaceId /
ownerSystem / action / resourceRef / workflowId / runId / applicationId / processInstanceId
```

日志只记录稳定 ID、状态、attempt 和 allowlist error class。指标至少包括 command backlog/age、
Inbox duplicate/conflict、result Outbox retry/dead、receipt terminal latency、approval materialization lag、
decision delivery latency 和 reconciliation finding。禁止把 tenant、resource ID 或 operation ID 作为
无限基数 metrics label。

管理 replay/retry 必须要求 operator 权限、原因和 Idempotency-Key；保留原 operation/event/request ID。
所有 DEAD、冲突和乱序结果均需可查询，但管理端不得提供“直接把状态改成功”的入口。

## 11. 发布顺序

1. 发布共享 v1 contract 和兼容性门禁，不启用 consumer。
2. 建 owner Inbox/result Outbox migration，部署 relay 但保持 feature flag 关闭。
3. 初始化 durable consumer 和精确 NATS 权限。
4. 启用单一 Fluxion tenant 的 `project.annotate` policy，完成 live 故障矩阵。
5. 灰度 Fluxion → Approver 低置信度派单审批，保留本地 human task fallback。
6. 扩展 Approver/Bids annotation adapter。
7. 完成容量、备份恢复、凭据轮换和升级/回滚验收后，才进入 Beta 准入评审。

## 12. 明确延期

- 跨 MongoDB/PostgreSQL/NATS/Temporal/Conductor 的 XA、2PC 或 exactly-once 宣称；
- Record Hub 直接修改 owner 数据库或直接 Signal/Update owner workflow；
- 将 Approver 包导入 Fluxion/Bids，或把 Approver workflow 作为跨引擎 child workflow；
- Bids 报价、投标正文、密封文件、定标、合同签署和付款 command；
- 通用脚本/函数运行时、任意表自动写回和用户自定义 NATS subject；
- 未经过独立评审的生产 Dex workload identity、生产 HA 和多地域承诺。
