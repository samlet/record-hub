# Phase 3 真实业务系统接入验收计划

- 日期：2026-09-18
- 状态：Gate A contract baseline complete; live owner gates planned
- 需求：[phase-3-requirements.md](phase-3-requirements.md)
- 任务：[phase-3-task-breakdown.md](phase-3-task-breakdown.md)

## 1. 验收原则

Phase 3 必须使用真实 MongoDB、NATS JetStream、Dex、workload issuer、PostgreSQL、Temporal、
Conductor 和四个应用进程。单元测试、fake transport、内存 repository 和直接写数据库只用于开发，
不能替代 live Gate。

每次验收生成独立 evidence directory，至少包含：

```text
manifest.json          组件版本、四仓库 commit、开始/结束时间、主机摘要
config-redacted.json   端口、issuer、audience、scope、数据库名、feature flag
results.json           case ID、PASS|FAIL|SKIPPED、耗时、稳定错误码
logs/                  每进程有界日志，secret/token/cookie 已脱敏
metrics/               验收前后 backlog、lag、dead、latency snapshot
db-assertions/         计数/hash/version/unique 断言，不保存敏感正文
```

`SKIPPED` 必须记录缺失依赖、风险、责任人和可执行重试条件；不能计入 Gate 通过。

## 2. 隔离拓扑

在现有 P2 原生拓扑上扩展，不停止或复用用户正在运行的共享服务：

| 组件 | 建议默认 | 要求 |
| --- | --- | --- |
| MongoDB replica set | `127.0.0.1:37018` | 临时 dbpath、专用数据库 |
| NATS JetStream | `127.0.0.1:14223` / monitor `:18223` | 临时 store、独立 users/ACL |
| Dex human issuer | `http://127.0.0.1:15566/dex` | 临时 SQLite、四 Web clients |
| workload issuer | `http://127.0.0.1:15557/workload` | 临时 key、每方向独立 client |
| Temporal | gRPC `:17233`, UI `:18233` | 临时 SQLite/namespace |
| Conductor | HTTP `:18080` | 临时 local server |
| Record Hub | `127.0.0.1:18081` | 专用 Mongo/NATS/issuer |
| Approver API/Worker | `127.0.0.1:18090` / 独立 worker | 专用 PostgreSQL schema/DB |
| Fluxion API/Worker | `127.0.0.1:18091` / 独立 worker | 专用 PostgreSQL schema/DB |
| Bids API/Worker | `127.0.0.1:18092` / 独立 worker | PostgreSQL，不用 SQLite 代替 Gate |

所有端口可通过 `RECORD_HUB_P3_*` 覆盖。启动脚本先检查端口和二进制；任一冲突应 fail fast，
不得杀死未知 PID。随机 secret 只存在临时目录/进程环境；成功后删除，失败时保留目录但先脱敏。

当前已提供契约门禁入口；owner live 入口仍是后续批次目标：

```text
make p3-contract-gate        # 已实现：四仓库 command/result 镜像与 manifest hash
make p3-fluxion-command-live
make p3-fluxion-command-faults # 可选：双 worker、commit-before-ACK、结果消费者重启
make p3-approval-pilot-live
make p3-three-owner-live
make p3-full-topology
```

## 3. Gate A：契约与静态边界

| Case | 场景 | 通过条件 |
| --- | --- | --- |
| A-001 | 四仓库 manifest/hash | command/result/request/result assets 逐文件一致 |
| A-002 | 合法 fixture | Java/Kotlin/Go/Record Hub verifier 全部接受，canonical hash 一致 |
| A-003 | 未知字段/非法 UTF-8/尾随 JSON | 全部拒绝且错误不回显 payload |
| A-004 | 256 KiB 边界 | 上限内接受，超过上限在解析前拒绝 |
| A-005 | breaking mutation | 删除必填字段、改枚举语义、换类型时 CI 失败 |
| A-006 | 内部依赖扫描 | Fluxion/Approver/Bids 未导入 Record Hub server/internal 或 Mongo 模型 |
| A-007 | subject/ACL 静态检查 | 每个 principal 只拥有设计中允许的 publish/pull/ACK subject |

## 4. Gate B：Fluxion Command 正常路径

实现基线已完成（Fluxion commit `7811ddd`）：V8 Inbox/Outbox、strict decoder、durable pull runner、
`project.annotate` APPEND/VOID 和 result relay 均已接入。以下 live 验收仍需真实 PostgreSQL、NATS、
Record Hub 与 Fluxion worker 进程共同运行；单测不能替代 Gate B。

### B1 APPEND

核心正常路径已由 `make p3-fluxion-command-live` 在 2026-09-19 真实通过；故障矩阵由
`make p3-fluxion-command-faults` 在相同隔离拓扑中执行，不能以单测替代。

1. 使用 `fluxion-to-record-hub` token 提交 `project.annotate`，记录 operation ID。
2. 断言 receipt 从 `ACCEPTED`/`DISPATCHED` 最终进入 `SUCCEEDED`。
3. 断言 Fluxion 只有一条 Inbox、一条 annotation、一条 result Outbox。
4. 断言 Project summary version 恰好 +1，summary Outbox 使用该版本。
5. 断言 Record Hub 投影 source version 最终等于 owner result version。
6. 断言 Temporal history 和日志不包含 annotation text 原文。

### B2 VOID

1. 对已存在 annotation 提交 `mode=VOID`。
2. 断言只追加 void 记录，原记录未删除。
3. 重复相同 operation ID 不产生第二条 void。
4. 跨项目、未知 annotation、重复新 operation 的二次 void 均稳定拒绝。

## 5. Gate C：Fluxion Command 故障矩阵

| Case | 注入点 | 预期 |
| --- | --- | --- |
| C-001 | 相同 operation/hash 重发 | 返回原 receipt；owner 副作用计数仍为 1 |
| C-002 | 相同 operation/不同 hash | `IDEMPOTENCY_CONFLICT`；owner 不执行 |
| C-003 | expected version 过期 | owner `REJECTED/VERSION_CONFLICT`；不改变 Project |
| C-004 | command publish 前 Record Hub 停止 | receipt 保持可恢复状态；同 key 重试只一条 operation |
| C-005 | owner transaction 前崩溃 | command 重投后正常执行一次 |
| C-006 | owner transaction 中途回滚 | Inbox/domain/result Outbox 均不提交 |
| C-007 | commit 后 ACK 前崩溃 | 重投读取已完成 Inbox；不重复 annotation（live fault matrix 已覆盖） |
| C-008 | result publish 后 mark SENT 前崩溃 | 同 event ID 重发；Record Hub 幂等终态 |
| C-009 | Record Hub result consumer 停止 | owner result Outbox/JetStream 可见；恢复后 receipt 收敛（live fault matrix 已覆盖） |
| C-010 | NATS outage | owner/Record Hub 本地事实保留；恢复后 backlog 清空（live fault matrix 已覆盖） |
| C-011 | 非法 owner/action/tenant/workspace | fail closed；不泄露资源存在性 |
| C-012 | slow/poison message | 有界重试后 DLQ；其他正常消息继续推进（live fault matrix 已覆盖） |
| C-013 | 两个 owner worker 竞争 | unique/CAS 保证一条领域副作用（live fault matrix 已覆盖） |
| C-014 | 两个 Record Hub result worker 竞争 | operation revision 单调，终态唯一（live fault matrix 已覆盖） |

跨系统同时重启也由 `make p3-fluxion-command-faults` 覆盖：command 已 dispatch 后同时停止 Record Hub
与 Fluxion worker，再恢复 Record Hub、owner worker，验证 durable command/result consumer 不丢失且只产生一条领域副作用。

每个 case 在结束时比较：Record Hub operation、JetStream consumer state、Fluxion Inbox、annotation、
summary Outbox、result Outbox 和 projection version。只检查 HTTP 200/202 不算通过。

## 6. Gate D：Fluxion → Approver 审批试点

### D1 正常终态

| Case | Approver 终态 | Fluxion 最终断言 |
| --- | --- | --- |
| D-001 | APPROVED | 原 proposal hash/generation 验证后被采用，Workflow 继续 |
| D-002 | REJECTED | 走固定策略兜底，仅一次派单推进 |
| D-003 | WITHDRAWN/CANCELLED | external request 关闭，创建一条本地人工任务 |
| D-004 | EXPIRED | 不采用 proposal，创建人工任务并产生告警 |
| D-005 | FAILED | Workflow 不静默成功；有界重试后人工兜底 |

所有场景断言：一条 Fluxion request、一条 Approver Application、一个 Process、一个 decision version、
一条 Fluxion result Inbox，Record Hub 中的 Application/Project 安全投影可通过 stable refs 关联。

### D2 故障与竞争

| Case | 场景 | 通过条件 |
| --- | --- | --- |
| D-010 | Fluxion request POST 响应丢失 | 使用原 external request ID 查询；无第二 Application |
| D-011 | Approver materializer 重启 | lease 恢复；Application key 唯一 |
| D-012 | Approver result delivery 重复 | Fluxion decision version 去重；只一次 Temporal update |
| D-013 | result transaction 后 update 前崩溃 | Temporal Outbox 恢复后推进一次 |
| D-014 | 已改派后晚到 APPROVED | 拒绝 Apply，生成 finding，不覆盖新 generation |
| D-015 | Project 取消与决定并发 | 行锁/状态机决定唯一结果，不恢复已取消项目 |
| D-016 | Approver outage 超过业务 timeout | 转本地人工兜底；在途外部申请被取消/对账，不双审批 |
| D-017 | feature flag 中途关闭 | 新 generation 走本地；旧申请继续可管理 |
| D-018 | Temporal replay/Continue-As-New | 不重发申请；pending external request ID 不丢失 |
| D-019 | credential rotation | 双方向新旧 overlap 成功，旧 secret 到期拒绝 |

## 7. Gate E：三 Owner Adapter

同一 fixture matrix 分别运行：

| Owner | Command | 禁止副作用 |
| --- | --- | --- |
| Fluxion | `project.annotate` | workflow stage、payment、assignment |
| Approver | `application.annotate` | Application status、Process state、Task decision |
| Bids | `tender.annotate` | bid/quote/file/opening/award/contract/payment |

除 owner-specific schema/version/tenant mapping 外，三套实现必须对 duplicate、hash conflict、version
conflict、commit-before-ACK、result retry 和 restart 给出相同协议结果。关闭一个 owner worker 不得影响
其他 durable consumer。

## 8. Gate F：恢复、容量和升级

### F1 备份恢复

- MongoDB：schemas、records、operations、read pointers、projection receipts/indexes。
- PostgreSQL：owner Inbox、领域事实、domain/result Outbox、Approver request/result、审计。
- JetStream：stream/consumer 配置、retention、durable cursor；消息数据按已声明 RPO 评估。

恢复后验证 count、canonical hash、unique/index、最高 aggregate/record/result version、未完成 backlog，
并重启 worker。已完成 command/approval 不得再次产生业务副作用。

### F2 容量基线

至少记录：

- 10,000 个 command operations，1,000 个并发在途 operation；
- 每 owner 10,000 条 Inbox/Outbox 历史；
- steady 50 msg/s、burst 200 msg/s 的 command/result；
- 100 个并发审批等待；
- 正常 p50/p95/p99 terminal latency；
- 停止 consumer 10 分钟后的 backlog drain time；
- CPU、RSS、PostgreSQL/Mongo storage 和 NATS file store 增长。

这些是 Beta 测试起点，不是生产容量承诺。若本机资源不足，可降低规模但必须标记 `SKIPPED`/偏差，
不能把较小结果外推为生产结论。

### F3 升级/回滚

1. 先部署 additive DB migration 和兼容 reader。
2. 再发布 contract-aware producer/consumer，保持 feature flag 关闭。
3. 灰度单 tenant，观察至少一个完整 retention/retry window。
4. 回滚 worker 时，新表/字段保留，旧 worker 必须忽略 additive 数据。
5. 禁用新 policy/flag 停止新流量；在途 operation/request 由当前版本 drain 或显式取消。
6. 只有所有旧版本退出后才能执行 contract/migration 收紧。

## 9. Gate 判定

| Gate | 必须通过 |
| --- | --- |
| A | 全部 A cases |
| B/C | Fluxion 正常与故障矩阵全部通过，无重复副作用 |
| D | 五类终态、晚到结果、restart、fallback、rotation 全通过 |
| E | 三 owner 共享行为通过；高敏字段负向通过 |
| F | 恢复和升级通过；容量偏差被明确记录和接受 |

最终报告必须列出四仓库 commit SHA、运行时版本、硬件、配置摘要、所有 case 结果、遗留风险和 owner。
存在未解释的数据丢失、重复业务副作用、跨租户访问、敏感数据泄露或不可恢复 migration 时，Phase 3
直接判定 FAIL，不允许降级为 PARTIAL 后进入 Beta。
