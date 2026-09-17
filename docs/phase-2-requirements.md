# 下一期需求设计：Integration Beta

日期：2026-09-17
状态：Draft for review
依据：[下一期方案评估](phase-2-evaluation.md)

## 1. 目标与成功标准

下一期要把 Record Hub 从 MVP 中间件提升为可供 Approver、Fluxion、Bids 持续接入的 Beta
平台，并完成一个受控命令与一个审批集成垂直切片。

成功必须同时满足：

- MVP 中所有 `SKIPPED` live 项在隔离拓扑中补跑通过，或经评审明确移出 Beta 范围；
- 三个 producer、两个 workflow engine、Record Hub 和身份服务可一键启动、停止并保留证据；
- Schema 变更、Projection rebuild、实时订阅和 command operation 均可审计、重试和恢复；
- 不引入跨数据库事务承诺，不绕过 owner system，不在 workflow history 保存可变记录正文；
- 一个真实审批旅程完成正常、拒绝、撤回、超时和重复回调验收。

## 2. 角色与核心旅程

| 角色 | 核心旅程 |
| --- | --- |
| Workspace Owner | 注册 source/mapping，发布 Schema，审批迁移计划，发起 rebuild，查看审计 |
| Editor | 创建/编辑自有记录和 View，订阅授权范围内的变更 |
| Viewer | 查询记录、投影 freshness 和只读实时变化 |
| Workflow service | 以 workload identity 获取 immutable snapshot、提交受控 command、查询 receipt |
| Source owner system | 事务消费 command、执行业务规则、发布 result/domain event |
| Operator | 观察 lag/backlog/DLQ，执行 replay/rebuild/rotation/restore runbook |

## 3. 功能需求

### 3.1 身份与授权（P2-SEC）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-SEC-001 | 冻结 human/workload identity ADR | 明确 issuer、audience、subject、purpose、rotation、revocation 和本地/生产差异；禁止 password grant |
| P2-SEC-002 | 支持独立 workload issuer | 每个服务使用独立 client/subject；token 有短 TTL、精确 audience/scope；跨 workspace/resource/purpose 全拒绝 |
| P2-SEC-003 | 统一 principal contract | human 与 workload 映射到不同 principal kind；审计保留 `(iss, sub)`，不以 email/client name 作主键 |
| P2-SEC-004 | 凭据滚动轮换 | 新旧 key overlap 期间不中断；旧 key 过期后 fail closed；JWKS outage cache 边界有 live 证据 |
| P2-SEC-005 | 管理操作 step-up policy | source、mapping、migration、rebuild、command policy 仅 OWNER/受权 operator 可执行并写审计 |

### 3.2 Schema 与数据产品（P2-DATA）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-DATA-001 | Schema compatibility diff | 发布前返回字段级 additive/breaking diff、原因和 JSON pointer；结果可重复 |
| P2-DATA-002 | 显式迁移计划 | breaking 版本必须引用 migration plan；支持 dry-run、受影响记录计数、失败样本上限和取消 |
| P2-DATA-003 | 迁移幂等与恢复 | 每条记录以 operation ID/CAS 迁移；中断后续跑不重复副作用；旧 snapshot/history 不被重写 |
| P2-DATA-004 | View 生命周期 | View 支持版本化 create/update/delete、共享范围与 OWNER/EDITOR 权限；冲突返回当前版本 |
| P2-DATA-005 | 查询成本保护 | limit、filter/sort/index allowlist、执行时间、响应大小和并发均有界；拒绝任意 Mongo query |
| P2-DATA-006 | 数据导出 | workspace-scoped NDJSON/CSV 异步导出，带审计、过期和字段授权；projection 敏感字段仍禁止导出 |

### 3.3 Source、Mapping 与 Projection（P2-PROJ）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-PROJ-001 | Source registration | source system、event type/version、tenant/workspace resolution 和 owner 联系方式显式登记且版本化 |
| P2-PROJ-002 | Mapping registry | event→record mapping 有 schema、版本、canonical hash、fixture 和发布审批；runtime 只运行已发布版本 |
| P2-PROJ-003 | Replay/rebuild operation | projector 先将原始 envelope 写入 scope-scoped archive；rebuild 使用独立 staging generation/records，按 record version 单调 replay，补齐 checkpoints，并在校验通过后原子切换 read pointer；失败/取消不得暴露 staging |
| P2-PROJ-004 | Gap/DLQ remediation | UI/API 展示安全摘要、建议动作和关联 operation；重放必须使用原 event ID，不允许手改 projection |
| P2-PROJ-005 | Freshness SLO | 每 source 暴露 last event、projected version、lag age、backlog、error budget；超阈值可告警 |

### 3.4 Realtime read feed（P2-RT）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-RT-001 | 提供 SSE 只读变更流 | 按 tenant/workspace/table 授权；事件只含 record reference、version、change type 和安全字段 |
| P2-RT-002 | 断线恢复 | 支持有界 cursor/`Last-Event-ID`；cursor 过旧返回明确 reset/requery 信号，不承诺无限历史 |
| P2-RT-003 | 背压与配额 | 每 principal/workspace 连接数、速率、缓冲和事件大小有界；慢消费者被安全断开 |
| P2-RT-004 | 授权变化生效 | membership 撤销后连接在有界时间内终止；跨租户订阅和 cursor 复用负向测试通过 |

### 3.5 Workflow Binding 与 Command Gateway（P2-CMD）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-CMD-001 | Binding SDK 稳定化 | Go/Java/TypeScript SDK 版本化；timeout、retry、error taxonomy 和兼容矩阵发布 |
| P2-CMD-002 | Command policy registry | 只允许显式注册的 owner/resource/action/schema；策略绑定 purpose、workspace 和 expected version |
| P2-CMD-003 | Operation receipt | 提交返回稳定 operation ID；状态至少含 ACCEPTED/DISPATCHED/SUCCEEDED/REJECTED/FAILED/EXPIRED |
| P2-CMD-004 | Owner-system Inbox | command 与业务变更同事务 claim/commit；重复 command ID 返回原结果；payload hash 冲突拒绝 |
| P2-CMD-005 | 结果与冲突 | owner 发布 result event；expected-version 冲突可诊断；Record Hub 不替 owner 自动覆盖 |
| P2-CMD-006 | 故障语义 | ACK loss、重复、乱序、timeout、owner outage、result loss 和重启均有 live E2E；无 exactly-once 宣称 |
| P2-CMD-007 | 首个垂直切片 | 选择一个可逆、低敏、低风险动作；禁止审批决定、报价/投标、资金或文件权限作为首个动作 |

### 3.6 Approver 集成（P2-APR）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-APR-001 | 独立服务 connector | Fluxion/Bids 通过 Approver API/SDK 调用，不导入其领域实现包，不以跨引擎 child workflow 耦合 |
| P2-APR-002 | 稳定审批关联 | 保存 owner resource ref、workflow run ref、application ID、operation ID 和 snapshot hash；不复制审批事实 |
| P2-APR-003 | 幂等申请与回调 | retry 使用原 operation ID；重复创建/回调无重复审批或状态推进；payload hash 冲突拒绝 |
| P2-APR-004 | 审批状态投影 | Record Hub 展示安全摘要与 freshness；审批动作仍只在 Approver 执行 |
| P2-APR-005 | 完整结果矩阵 | approved/rejected/withdrawn/expired/cancelled、晚到回调和 owner restart 均有 live E2E |
| P2-APR-006 | 首个试点 | 只选一个 Fluxion 或 Bids 低风险节点；上线前由业务 owner 确认字段、SLA 和补偿策略 |

### 3.7 Web 与运维（P2-OPS）

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| P2-OPS-001 | 集成管理 UI | source/mapping/migration/rebuild/operation 页面均显示版本、状态、actor、时间和安全错误摘要 |
| P2-OPS-002 | 隔离监督式拓扑 | 无端口/数据库复用；一条命令启动、探测、运行测试并停止；失败保存有界日志与版本清单 |
| P2-OPS-003 | 备份恢复演练 | Mongo backup/restore 与 JetStream retention/replay runbook 实测；记录 RPO/RTO，不把本地单节点当 HA |
| P2-OPS-004 | SLO 与告警 | API、projection、realtime、command、approval 各有可测 SLI/阈值和告警去重策略 |
| P2-OPS-005 | 审计导出与保留 | 管理/写入/replay/command/identity 事件可按 workspace/time 导出；内容不含 token、secret 或原始敏感 payload |
| P2-OPS-006 | 升级/回滚 | API、event、mapping、SDK、Mongo index 的兼容窗口和回滚步骤文档化并演练 |

## 4. 非功能需求

- 所有同步 API 默认有 timeout，分页上限 100，payload 与响应上限显式配置并设安全硬上限。
- 所有消息处理至少一次；幂等以 operation/event ID + canonical request hash 实现。
- 日志、指标、trace 和 DLQ 摘要不得含 token、cookie、报价、投标正文、PII 或文件 URL。
- 新的 Mongo 写路径必须有 transaction/CAS/unique-index 证据；新的 NATS consumer 必须 durable、
  有 MaxDeliver/backoff/DLQ 和 bounded drain。
- Beta 容量基线必须记录数据规模、事件率、SSE 连接数、p95/p99、backlog recovery time 和测试硬件。
- 所有对外契约必须 OpenAPI/JSON Schema first，并提供至少 Go、Java、TypeScript 的兼容测试。

## 5. 验收矩阵

| 场景 | 必须 live | 通过条件 |
| --- | --- | --- |
| Dex 浏览器登录/角色/退出 | 是 | OWNER/EDITOR/VIEWER 和撤权矩阵无跳过 |
| workload token/rotation | 是 | audience/scope/purpose/expiry/旧 key 负向与滚动轮换通过 |
| Temporal + Conductor Binding | 是 | history 仅 ref/hash；timeout/retry/restart 后 snapshot 稳定 |
| 三 producer outage/recovery | 是 | Outbox 积压可见，恢复后清空，无丢失/重复副作用 |
| Projection rebuild | 是 | staging 校验、切换、取消、失败恢复和审计通过 |
| SSE | 是 | 断线恢复、慢消费者、撤权、跨租户负向通过 |
| Command | 是 | duplicate/ACK loss/conflict/owner outage/result loss/restart 通过 |
| Approval pilot | 是 | 五种终态、重复/晚到回调和补偿路径通过 |
| Backup/restore | 是 | 在声明 RPO/RTO 内恢复，并验证 hash/count/index/checkpoint |

不允许将单元测试、fake server、内存 repository、直接写 Outbox 或手工修改数据库作为上述
live 场景的替代证据。

## 6. 推荐任务批次

### Batch 0：关闭基础阻断

- P2-SEC-001..004、P2-OPS-002；
- 补跑 MVP `SKIPPED` 项；
- 输出 identity ADR 与基线验收报告。

### Batch 1：可运维数据平面

- P2-DATA-001..005、P2-PROJ-001..005；
- Schema migration 与 projection rebuild 垂直切片；
- 管理 UI、审计和 SLO 基础。

### Batch 2：只读实时能力

- P2-RT-001..004；
- SDK subscription helper、Web 自动刷新、连接与速率基线。

### Batch 3：受控命令

- P2-CMD-001..007；
- 一个 owner system 的低风险动作；
- 完整故障矩阵后才允许第二个 command。

### Batch 4：审批试点与 Beta 准入

- P2-APR-001..006、P2-OPS-003..006；
- 完成容量、安全、恢复、升级/回滚和 Beta 验收报告。

## 7. 开始实施前仍需业务确认

以下选择会改变领域设计，不能由技术实现自行猜测：

1. 首个受控 command 的 owner system、resource/action、可逆性和补偿责任人；
2. 首个审批试点选择 Fluxion 还是 Bids，以及审批输入字段、SLA 和五种终态映射；
3. Beta 的目标 RPO/RTO、峰值事件率、记录量、SSE 并发和数据保留期；
4. workload identity 在本地/Beta 环境采用的 Authorization Server，以及生产是否规划 SPIFFE。
