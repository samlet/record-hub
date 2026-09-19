# Phase 4 Integration Beta 验收计划

- 日期：2026-09-20
- 状态：Planned
- 方案：[phase-4-design.md](phase-4-design.md)
- 需求：[phase-4-requirements.md](phase-4-requirements.md)
- 任务：[phase-4-task-breakdown.md](phase-4-task-breakdown.md)

## 1. 验收原则

Phase 4 使用真实 MongoDB、NATS JetStream、Dex/候选 workload issuer、PostgreSQL、Temporal、Conductor
和四个应用进程。单元测试、fake transport、内存 repository 或直接改业务表不能替代 live Gate。

每个 case 只能是 `PASS`、`FAIL` 或 `SKIPPED`：

- `PASS`：真实依赖、完整断言和 evidence 均存在；
- `FAIL`：期望未满足或证据不完整；
- `SKIPPED`：依赖不可用，必须记录风险 owner 和可执行重试条件，且不计入 Gate 通过。

Phase 4 最终 Beta Gate 的必选 case 不接受 `SKIPPED` 或缩小断言后的替代 PASS。

## 2. Evidence 规范

默认目录为 `build/evidence/phase4/<run-id>`，可由环境变量指向 CI artifact 目录。每次运行至少包含：

```text
manifest.json             run ID、时间、主机摘要、四仓库 commit、artifact checksum
config-redacted.json      端口、issuer、audience、scope、DB 名、feature flags，无 secret
results.json              case ID、PASS|FAIL|SKIPPED、耗时、错误分类、重试条件
checksums.sha256          本目录受控文件 hash
logs/                     每进程截断、脱敏日志
metrics/                  前后 backlog/lag/dead/latency/resource snapshots
db-assertions/            count/hash/index/version/cursor，不保存敏感正文
security/                 tenant/ACL/field/secret scan 结果
recovery/                 recovery-set manifest、restore assertions、RPO/RTO
release/                  old/new artifact、migration、rollback manifest
```

evidence 目录不提交 Git；CI/发布流水线按待确认的 retention policy 保存。报告必须记录 artifact identity，
不能只引用可能被系统清理的 `/tmp` 路径。

## 3. 隔离拓扑

建议端口只作为默认值，所有值均可用 `RECORD_HUB_P4_*` 覆盖：

| 组件 | 建议默认 | 隔离要求 |
| --- | --- | --- |
| MongoDB replica set | `127.0.0.1:47018` | 专用 dbpath/source DB；另建 restore target |
| NATS JetStream | client `:24223`、monitor `:28223` | 专用 store/account/users；另建 restore store |
| Dex human issuer | `http://127.0.0.1:25566/dex` | 四个独立 Web clients、临时数据库 |
| workload issuer | `http://127.0.0.1:25557/workload` | reloadable key/client registry、每方向独立 client |
| Temporal | gRPC `:27233`、UI `:28233` | 专用 namespace/storage |
| Conductor | HTTP `:28080` | 专用 execution domain |
| Record Hub | `127.0.0.1:28081` | 专用 Mongo DB/NATS consumers |
| Approver API/Worker | `127.0.0.1:28090` / 独立 worker | 专用 PostgreSQL DB/schema |
| Fluxion API/Worker | `127.0.0.1:28091` / 独立 worker | 专用 PostgreSQL DB/schema |
| Bids API/Worker | `127.0.0.1:28092` / 独立 worker | PostgreSQL，不以 SQLite 代替 Gate |

Supervisor 启动前检查端口和 binary，不能 kill 未知 PID。退出只停止本次 run 启动的进程；恢复/回滚
case 的 source、restore、old/new 目标在 evidence 完成前不得自动删除。

## 4. Gate A：契约、基线与证据

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| A-001 | P4-BAS-001 | 四仓库 release baseline | commit、artifact、migration、contract、build command 全部固定 |
| A-002 | P4-BAS-004、P4-BAS-005 | mirror/hash/compatibility | 所需 schema/fixture byte-identical；breaking mutation 被拒绝 |
| A-003 | P4-BAS-003 | evidence integrity | manifest/config/results/log/metrics/assertions/checksum 完整且可上传 |
| A-004 | P4-BAS-006 | fake 依赖防替代 | live Gate 检测 fake/in-memory/SQLite/direct mutation 并拒绝 |
| A-005 | P4-SEC-008 | evidence secret scan | token/cookie/secret/PII/high-sensitive fixtures 均无发现 |

## 5. Gate B：Phase 3 收口

| Case | 需求 | 注入/动作 | 通过条件 |
| --- | --- | --- | --- |
| B-001 | P4-SEC-003 | issuer 增加新 key，旧 token 仍在 overlap | 新旧 token 按窗口验证；过窗旧 key fail closed |
| B-002 | P4-SEC-004 | 逐方向轮换 client secret 并 reload owner | 无请求丢失、重复副作用或长期鉴权失败 |
| B-003 | P4-OPS-001、P4-OPS-002 | 备份 Mongo/三 PostgreSQL/JetStream 并恢复隔离目标 | count/hash/index/version/cursor/backlog 对齐 |
| B-004 | P4-OPS-003 | 记录 backup point 到恢复业务可用 | 实测 RPO/RTO 满足已签字目标，否则 FAIL |
| B-005 | P4-OPS-004..006 | 四 owner mixed load + 停 consumer 10 分钟 | 规模、SLO、drain、storage、重复副作用完整记录 |
| B-006 | P4-OPS-007 | old→new rolling deploy→old rollback | additive 数据兼容、durable 不重建、在途 request 可管理 |
| B-007 | P4-BAS-002 | Fluxion transaction 中途 rollback | Inbox/domain/result Outbox 均不提交，重投只成功一次 |
| B-008 | P4-BAS-002 | result publish 后 SENT 前 crash | 同 event ID replay，Record Hub terminal revision 唯一 |
| B-009 | P4-BAS-002 | Approver/Fluxion/Bids 同时运行 behavior fixtures | duplicate/hash/version/restart/ACK-loss 语义一致 |

## 6. Gate C：Fluxion Approval Beta

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| C-001 | P4-FLX-001 | flag 默认关闭/错误 tenant | 不创建 request；不泄露 policy/资源存在性 |
| C-002 | P4-FLX-002、P4-FLX-003 | approve 正常路径 | 一条 request/Application/result/Temporal update，stable IDs/hash 对齐 |
| C-003 | P4-FLX-004 | reject | Fluxion 走拒绝分支，不执行派单副作用 |
| C-004 | P4-FLX-004 | withdraw/cancel | 双方终态一致；重复取消幂等 |
| C-005 | P4-FLX-004 | expire | 有界等待后过期；不无限挂起或晚执行 |
| C-006 | P4-FLX-004 | safe failure | allowlist error；无 stack/token/payload 泄漏 |
| C-007 | P4-FLX-005 | generation 改变后晚到 approve | 不覆盖新事实；产生 VERSION_DRIFT/late-result finding |
| C-008 | P4-FLX-006 | 关闭 flag 且有在途 request | 不创建新请求；旧请求 drain/cancel 可追踪 |
| C-009 | P4-FLX-007 | Approver outage | 按 policy 等待/expire/local fallback；同 generation 不双写 |
| C-010 | P4-FLX-008 | Temporal replay/Continue-As-New | 原 request ID 被携带，无重复 Application/update |
| C-011 | P4-RH-001、P4-RH-002、P4-RH-003、P4-RH-004 | Project↔Application projection | typed refs、version、gap/conflict、tenant auth 和 lag 正确 |
| C-012 | P4-RH-005、P4-RH-006 | request/result/workflow 状态不一致 | finding 分类正确；operator retry 不直接改终态 |

## 7. Gate D：Bids Approval Pilot

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| D-001 | P4-BID-001、P4-BID-002、P4-BID-003 | 招标准备发布 approve | 仅 allowlist metadata；Application 与 Bids request/hash 对齐 |
| D-002 | P4-BID-004、P4-BID-005 | result 正常应用 | Result Inbox、Bids 状态、task Outbox 同事务 |
| D-003 | P4-BID-006 | Conductor task retry/worker restart | 原 request ID，Application 与 task completion 均一次 |
| D-004 | P4-BID-007 | tender version 已推进 | 旧决定不改变当前事实，写 VERSION_DRIFT finding |
| D-005 | P4-BID-004 | duplicate/same hash | 返回原终态，无第二个领域或 workflow 副作用 |
| D-006 | P4-BID-004 | same ID/different hash/terminal | 冲突 fail closed，进入人工 finding |
| D-007 | P4-BID-008 | cross-org/tenant | 403/稳定拒绝，不泄露资源存在性 |
| D-008 | P4-SEC-007 | 注入报价、投标正文、文件、评分、合同、付款字段 | schema/handler 在持久化和日志前拒绝 |
| D-009 | P4-BID-008 | unknown/oversize/malformed payload | 有界错误或 safe DLQ；后续正常消息推进 |
| D-010 | P4-RH-001、P4-RH-002、P4-RH-003、P4-RH-004 | Tender↔Application projection | 安全字段、org/tenant auth、version/gap/finding 正确 |
| D-011 | P4-BID-009 | external/local flag 切换且存在在途申请 | 同 generation 只有一个可决定任务；新流量切换，旧申请可 drain/cancel |

## 8. Gate E：租户、权限与数据安全

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| E-001 | P4-SEC-001、P4-SEC-002 | Human token 调 workload API；workload token 调 Web API | 双向拒绝，audience/scope 错误稳定 |
| E-002 | P4-SEC-006 | tenant A token 读取/提交 tenant B refs | API/message/projection/association 全部拒绝 |
| E-003 | P4-SEC-005 | owner 使用越权 NATS subject/durable | server ACL 拒绝；不能绕过预创建 durable |
| E-004 | P4-RH-003 | Viewer/Editor 执行 operator recovery | 403；Operator 操作要求原因与 Idempotency-Key |
| E-005 | P4-SEC-007/008 | 扫描 Temporal/Conductor history、log、metrics、DLQ、evidence | 无 token、PII、高敏 Bids 数据或原始审批正文 |
| E-006 | P4-RH-007 | Record Hub deep link | 仅跳转；无代理 decision endpoint 或越权 session 传递 |

## 9. Gate F：容量、恢复与可观测性

固定测试硬件、进程版本、worker concurrency 和数据规模后执行：

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| F-001 | P4-OPS-004 | 每 owner 10,000 history + steady 50 msg/s | 无未解释丢失/重复；记录 p50/p95/p99 和资源 |
| F-002 | P4-OPS-004 | burst 200 msg/s | backpressure 有界，无 OOM/无界 queue |
| F-003 | P4-OPS-004 | 100 并发 approval request | terminal/materialization/delivery latency 与错误率完整 |
| F-004 | P4-OPS-005 | 正常 projection/command | 对已签字 p95/p99 目标判定 PASS/FAIL，不隐藏偏差 |
| F-005 | P4-OPS-006 | consumer pause 10 分钟后恢复 | backlog drain time、oldest age、storage growth、重复副作用完整 |
| F-006 | P4-OPS-008 | 触发 backlog/dead/finding/recovery 告警 | label 有界，告警指向正确 owner/runbook |
| F-007 | P4-OPS-009 | 完整 evidence 校验 | checksum、redaction、artifact upload 与报告引用通过 |

## 10. Gate G：发布与回滚

| Case | 需求 | 场景 | 通过条件 |
| --- | --- | --- | --- |
| G-001 | P4-OPS-007 | additive migration + new reader/producer | 旧 worker 仍可运行且忽略新增字段 |
| G-002 | P4-OPS-007 | 单 tenant 打开新 flags | 非目标 tenant 无变化；目标 tenant 指标/finding 可见 |
| G-003 | P4-OPS-007 | 关闭 flags、drain/cancel 在途请求 | 无 orphan request/workflow，不删除新表/字段 |
| G-004 | P4-OPS-007 | 回滚到 immutable old artifacts | contract v1、durable cursor、Inbox/Outbox 与 projection 收敛 |
| G-005 | P4-OPS-010 | Beta observation window | 至少一个完整 retention/retry window 无未解释高风险 finding |
| G-006 | P4-OPS-010 | 最终报告 | commit/artifact/case/RPO/RTO/capacity/risk owner/rollback/签字完整 |

## 11. 最终判定

P4-G0..G5 必须全部通过，且 A～G 的所有必选 case 均为 `PASS`。以下任一项直接判定 Beta Gate
`FAIL`：

- 未解释的数据丢失、重复领域副作用、terminal conflict 或不可恢复 migration；
- 跨租户访问、secret/PII/Bids sealed data 泄漏；
- restore 后 count/hash/index/version/cursor 不一致；
- credential rotation 或 old-worker rollback 失败；
- 必选 live case 为 `SKIPPED`/`PARTIAL`；
- 报告未记录实际 commit/artifact、硬件、RPO/RTO、容量、风险 owner 或回滚条件。

通过 Phase 4 只授权受限 Beta tenant、受限 policy 和已验收业务切片。扩大租户、开放高敏动作、删除
fallback 或进入生产 GA 均需新的 Phase 5 决策与验收。
