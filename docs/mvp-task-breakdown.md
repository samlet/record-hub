# Record Hub MVP 任务分解

- 状态：Planning
- 设计：[mvp-design.md](mvp-design.md)
- 主仓库：`/Users/xiaofeiwu/apps/record-hub`
- 联合仓库：Approver、Fluxion、Bids

## 1. 状态定义

- `TODO`：未开始。
- `IN_PROGRESS`：正在实现。
- `PARTIAL`：核心存在，但验收、测试或文档不完整。
- `DONE`：实现和验收证据完整。
- `BLOCKED`：存在明确外部阻断。
- `DEFERRED`：明确不属于本 MVP 完成条件。

跨仓库任务分别提交，不制造跨仓库原子提交。每个联合验收记录各仓库 commit 和契约 hash。

## 2. M0：工程与契约基线

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M0-001 | Record Hub | 初始化 Go 1.27 module、模块化目录和统一命令入口 | DONE | - | module path、模块边界、build/test/lint/check 和 `record-hub` 命令入口已实现 |
| RH-M0-002 | Record Hub | 配置加载、结构化日志、graceful shutdown | DONE | 001 | api/worker/all 三种模式；配置缺失 fail closed；JSON 日志和有界退出已测试 |
| RH-M0-003 | Record Hub | `/healthz`、`/readyz` 与依赖探针 | DONE | 002 | readiness 区分 Mongo/NATS/Dex；响应不泄露地址、凭据或底层错误；适配器接入前 fail closed |
| RH-M0-004 | Record Hub | OpenAPI-first 基线与错误 envelope | DONE | 001 | spec lint 已纳入 check；统一错误 envelope；Go/Java/TS client 可重复生成 |
| RH-M0-005 | Record Hub | event envelope schema/fixture/verifier | DONE | 001 | valid/invalid fixture；大小、UTC 时间、未来偏移、版本、未知字段和尾随 JSON 测试 |
| RH-M0-006 | Record Hub | CI 与依赖/SBOM/secret scan | DONE | 001 | GitHub Actions 与本地 `make ci`；govulncheck、CycloneDX SBOM、Git 历史及工作树 secret scan |

## 3. M1：本地基础设施与身份

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M1-010 | Record Hub | MongoDB replica set 本地配置与初始化 | DONE | M0 | MongoDB 8.0.32；幂等 replica set/应用用户初始化；transaction、唯一索引、CAS、change stream smoke |
| RH-M1-011 | Record Hub | NATS JetStream stream/consumer 初始化 | DONE | M0 | NATS 2.14.6 file storage；DOMAIN_EVENTS、DEAD_LETTERS、三个 durable pull consumer 幂等创建及 smoke |
| RH-M1-012 | Record Hub | NATS users/subject 权限 | DONE | 011 | 独立 admin/三 producer/projector 身份；跨 source 发布、直接订阅及 topology mutation 负向 smoke |
| RH-M1-013 | Record Hub | Dex 本地 issuer 与四个 Web client | DONE | M0 | discovery/JWKS/code+PKCE/redirect 负向测试 |
| RH-M1-014 | Record Hub | Dex machine clients 与 token contract fixture | DEFERRED | 013 | Dex 2.45.1 尚未实现 `client_credentials`；不得以 password grant 代替，等待 machine identity ADR 或包含该能力的稳定版 |
| RH-M1-015 | Record Hub | OIDC verifier、JWKS cache 与 principal model | DONE | 013 | RS256；`(iss, sub)` identity；key rotation/cache/outage、错误 issuer/audience/expiry fail closed |
| RH-M1-016 | Record Hub | Workspace membership 与角色授权骨架 | DONE | 015 | 精确 tenant/workspace/`(iss, sub)` membership；OWNER/EDITOR/VIEWER allow/deny 矩阵通过 |

## 4. M2：Schema Registry

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M2-020 | Record Hub | SchemaDefinition persistence 与索引 | DONE | M1-010 | tenant/name/version 与 tenant/schemaId/version 唯一；draft CAS；published/deprecated immutable，真实 MongoDB 验收通过 |
| RH-M2-021 | Record Hub | Draft create/update/publish API | DONE | 020,M1-016 | REST/OpenAPI mutation endpoints；Idempotency-Key replay/conflict；If-Match revision CAS；OWNER/EDITOR authorization；Mongo transaction atomically commits schema、audit 和 receipt |
| RH-M2-022 | Record Hub | JSON Schema 2020-12 validation | DONE | 020 | 强制 Draft 2020-12；text/number/boolean/date-time/enum/reference 正负 fixture；非法 UTF-8/尾随 JSON 拒绝 |
| RH-M2-023 | Record Hub | compatibility checker | DONE | 022 | optional additive、required 放宽、integer→number/enum 扩展兼容；删除/改名/type/required/enum/约束收窄判 breaking |
| RH-M2-024 | Record Hub | semanticTypes/Schema.org URI mapping | DONE | 020 | Schema.org HTTPS canonicalization、HTTPS custom URI/URN、排序去重；不影响结构校验结果 |
| RH-M2-025 | Record Hub | canonical JSON/content hash | DONE | 022 | 确定性 UTF-8 key/number/string 规则、重复 key 拒绝、semanticTypes 规范化；Go/Java/TS 共享 fixture/expected hash |
| RH-M2-026 | Record Hub Web | Schema 列表、编辑、校验和发布 UI | TODO | 021 | 错误定位到字段路径；已发布只读 |

## 5. M3：多维表格核心

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M3-030 | Record Hub | Workspace/TableDefinition persistence/API | DONE | M1-016,M2 | tenant/workspace scope；Mongo unique indexes；published schema reference；CUSTOM/PROJECTION sourcePolicy 约束；OWNER 创建、成员只读访问 |
| RH-M3-031 | Record Hub | Record envelope、CRUD、schema validation | DONE | 030 | 自有 Record envelope；发布 schema 校验；Idempotency-Key replay/conflict；recordVersion/If-Match CAS；create/update/delete 与 audit 在 Mongo transaction 中提交 |
| RH-M3-032 | Record Hub | tag 与 typed relation | DONE | 031 | tags 规范化排序去重；稳定 system/type/id relation target；CURRENT/BROKEN/FORBIDDEN 状态；FORBIDDEN 不暴露 resolvedRecordId |
| RH-M3-033 | Record Hub | ViewDefinition、分页、排序、过滤 | DONE | 031 | ViewDefinition 持久化；字段/operator allowlist；复合排序键稳定 cursor；limit 1-100；Mongo 查询不接受任意 query |
| RH-M3-034 | Record Hub | 动态字段索引策略 | DONE | 033 | OWNER-only；仅已发布 schema 顶层字段；asc/desc；每表最多 16 个；确定性物理名；Mongo/API/HTTP/测试已覆盖 |
| RH-M3-035 | Record Hub | Projection record 写保护 | DONE | 031 | 通用 POST/PATCH/DELETE 在依赖、幂等键和版本校验前统一拒绝；HTTP 409 `PROJECTION_READ_ONLY`；负向矩阵已覆盖 |
| RH-M3-036 | Record Hub Web | workspace/table/grid/record detail UI | TODO | 030-033 | 六种字段、tag、排序过滤、列显隐 |
| RH-M3-037 | Record Hub Web | Projection 新鲜度和只读展示 | TODO | 035 | source/version/syncedAt/GAP 可见 |

## 6. M4：JetStream 消费与投影

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M4-040 | Record Hub | NATS client、durable pull lifecycle | DONE | M1-011,M0-002 | Go client 自动重连；durable pull Fetch；handler 成功后 DoubleAck、失败 NAK；取消时 bounded drain；配置有界且单元测试覆盖 |
| RH-M4-041 | Record Hub | InboxEvent claim/payloadHash 冲突 | DONE | 040,M1-010 | `(consumer,eventId)` 唯一 claim；相同 payload hash duplicate success；不同 payload 冲突；PROCESSING/APPLIED/REJECTED 状态；Mongo/内存测试已覆盖 |
| RH-M4-042 | Record Hub | 投影 handler registry | DONE | 041 | 精确 `sourceSystem/eventType/schemaVersion` 唯一注册；重复注册和 unknown event fail closed；并发安全单元测试已覆盖 |
| RH-M4-043 | Record Hub | projection transaction/checkpoint/audit | DONE | 042,M3-035 | Inbox APPLIED、projection record、checkpoint、audit 在同一 Mongo transaction；重复 APPLIED 幂等；Mongo/校验测试已覆盖 |
| RH-M4-044 | Record Hub | aggregate version duplicate/gap recovery | DONE | 043 | `<= current` 事件标记 Inbox APPLIED 并忽略副作用；跳跃版本保留 PROCESSING、checkpoint 标记 GAP；补齐后按序恢复；规则/真实 Mongo 测试已覆盖 |
| RH-M4-045 | Record Hub | retry/backoff/MaxDeliver/DLQ | DONE | 043 | deterministic/transient 分类；有界 backoff；按 delivery 次数与 MaxDeliver 终止；DLQ 仅含安全身份/原因不含 payload；fake runner 测试已覆盖 |
| RH-M4-046 | Record Hub | 三种 summary schema/handlers | DONE | 042,M2 | Draft 2020-12 `additionalProperties:false` allowlist；精确 source/type/version handler；256 KiB bound；PII/报价/文件 URL 负向测试 |
| RH-M4-047 | Record Hub | Operations events API/UI | DONE | 044,045 | `GET /api/v1/operations/events` 与 `/operations/events` 页面；积压/失败/gap 可查；固定字段投影，payload/secret 不泄露；查询有界并按 workspace 授权 |

## 7. M5：三个业务系统生产者

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M5-050 | 双仓库 | Approver ApplicationSummary v1 契约镜像 | DONE | M0-005,M4-046 | Approver 权威 schema/fixture 与 Record Hub 字节级一致；manifest hash 校验通过 |
| RH-M5-051 | Approver | Application summary Outbox 与事务写入 | DONE | 050 | 状态/安全摘要变化与业务状态同事务；稳定 `summary_version`、`eventId` 和 PII-free payload |
| RH-M5-052 | Approver | JetStream relay | DONE | 051,M1-012 | 专用 subject；publish ACK 后标记 SENT；ACK 丢失按原 event ID 重发 |
| RH-M5-053 | 双仓库 | Fluxion ProjectSummary v1 契约镜像 | DONE | M0-005,M4-046 | schema/fixture 字节级一致，跨语言契约测试通过 |
| RH-M5-054 | Fluxion | domain Outbox、Project 事务事件 | DONE | 053 | 不轮询 Temporal visibility；Project、project_events、Outbox 在同一事务写入 |
| RH-M5-055 | Fluxion | JetStream relay | DONE | 054,M1-012 | lease/retry/dead；ACK 丢失按原 event ID 重发；daemon 接入 API/Worker |
| RH-M5-056 | 双仓库 | Bids TenderSummary v1 契约镜像 | DONE | M0-005,M4-046 | schema/fixture 一致；投标/报价/联系人/文件 URL 等敏感字段负向校验通过 |
| RH-M5-057 | Bids | 扩展现有 Outbox 产生 domain event | DONE | 056 | 复用 `outbox_events`；`TENDER_SUMMARY_CHANGED` 与 Conductor/Finance command 分区隔离 |
| RH-M5-058 | Bids | JetStream relay | DONE | 057,M1-012 | 复用 lease/retry/dead；专用 subject、`Msg-Id` 和 ACK-loss 测试通过 |
| RH-M5-059 | 四仓库 | 三投影联合 E2E | PARTIAL | 052,055,058 | `scripts/verify-m5-producers.sh` 的跨语言契约、三 producer envelope/handler gate 通过；真实 Mongo/JetStream 只读表 E2E 待依赖启动后执行 |

## 8. M6：Workflow Binding

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M6-060 | Record Hub | snapshot persistence/API | DONE | M3,M2-025 | immutable snapshot store/API；同一 operation ID replay；不同请求 hash 冲突；canonical hash 覆盖 schema/record/source version 与 data |
| RH-M6-061 | Record Hub | machine client workspace/purpose policy | DONE | 060,M1-014 | 精确 issuer/subject/audience/tenant/workspace/resource/purpose allowlist；跨 scope、purpose、resource、audience 均拒绝；不伪造 Dex password grant |
| RH-M6-062 | Record Hub | Go client generation/binding facade | DONE | M0-004,060 | `sdk/go/recordhub` context-aware HTTP facade；bounded response、Idempotency-Key、timeout/cancel、typed API error |
| RH-M6-063 | Record Hub | Java client generation/binding facade | DONE | M0-004,060 | `sdk/java` Java 17+/Kotlin-callable facade；无 Temporal/Spring 强依赖；typed snapshot/error 与 timeout |
| RH-M6-064 | Fluxion | Temporal diagnostic Activity/Workflow | DONE | 063,M5-055 | history 仅 ref/hash；Activity retry 原 operation ID |
| RH-M6-065 | Fluxion | Temporal replay/failure tests | DONE | 064 | snapshot success、timeout、duplicate、replay |
| RH-M6-066 | Bids | Conductor diagnostic Worker/Workflow | DONE | 062,M5-058 | task output 仅 ref/hash；retry 幂等 |
| RH-M6-067 | 三仓库 | 双引擎 Binding E2E | PARTIAL | 065,066 | 跨仓库 contract gate 与 live smoke 脚本完成；真实 Record Hub/Mongo/Dex/双引擎重启恢复待依赖启动 |

## 9. M7：安全、可靠性与可观测性

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M7-070 | Record Hub | tenant/workspace/row/field 负向矩阵 | TODO | M3,M4,M6 | 用户与机器跨 scope 全拒绝 |
| RH-M7-071 | 四仓库 | secret/PII/sensitive payload scan | TODO | M5 | fixtures、日志、events、API response 无泄露 |
| RH-M7-072 | Record Hub | metrics 与 structured logging | TODO | M4 | 延迟、积压、重投、gap、DLQ、auth failure |
| RH-M7-073 | Record Hub | Mongo commit/ACK loss 故障注入 | TODO | M4 | 重投无重复记录/版本/审计 |
| RH-M7-074 | 四仓库 | NATS outage/outbox recovery | TODO | M5 | 业务事务继续；恢复后积压清空 |
| RH-M7-075 | Record Hub | Dex/JWKS rotation/outage | TODO | M1 | cache 边界、过期 fail closed、恢复成功 |
| RH-M7-076 | Record Hub | API/worker/Mongo/NATS 分进程重启 | TODO | M4,M6 | 无消息丢失，无永久 lease |
| RH-M7-077 | Record Hub | bounded query/payload/rate limit | TODO | M3,M4 | 大页、深过滤、大消息和滥用受控 |

## 10. M8：Web 与端到端验收

| ID | 仓库 | 任务 | 状态 | 依赖 | 验收 |
| --- | --- | --- | --- | --- | --- |
| RH-M8-080 | Record Hub Web | Dex 登录、Session、退出 | TODO | M1-013 | state/nonce/PKCE、cookie flags、CSRF |
| RH-M8-081 | Record Hub Web | workspace/table/schema 完整路径 | TODO | M2,M3,M8-080 | OWNER/EDITOR/VIEWER 浏览器矩阵 |
| RH-M8-082 | Record Hub Web | 三投影与 Operations 页面 | TODO | M4,M5,M8-080 | freshness/gap/DLQ 安全展示 |
| RH-M8-083 | 四仓库 | MVP happy-path E2E 脚本 | TODO | M5,M6,M8-081 | 一条命令重复执行结果一致 |
| RH-M8-084 | 四仓库 | MVP failure-path E2E 脚本 | TODO | M7 | outage、duplicate、gap、bad token、bad subject |
| RH-M8-085 | Record Hub | 本地运行与排障文档 | TODO | M8-083 | fresh machine 可按文档启动和验收 |
| RH-M8-086 | Record Hub | DLQ/gap/credential rotation runbook | TODO | M7 | 恢复步骤只使用原 event/operation ID |
| RH-M8-087 | 四仓库 | MVP 验收报告与 commit/hash 清单 | TODO | 083-086 | 证据、限制、遗留风险完整 |

## 11. M9：明确延期

| ID | 范围 | 任务 | 状态 | 进入条件 |
| --- | --- | --- | --- | --- |
| RH-M9-090 | Record Hub | 外部业务 Command Gateway | DEFERRED | 只读投影和 Binding 稳定 |
| RH-M9-091 | 联合 | Fluxion/Bids 审批迁移 | DEFERRED | Approver connector 方案单独评审 |
| RH-M9-092 | Record Hub | Storage Gateway | DEFERRED | 文件领域和权限模型冻结 |
| RH-M9-093 | Record Hub | Functions/sandbox runtime | DEFERRED | 威胁模型与隔离方案通过 |
| RH-M9-094 | Record Hub Web | Presence/Broadcast/协同光标 | DEFERRED | Realtime Gateway 容量方案通过 |
| RH-M9-095 | Record Hub | GraphQL/通用查询语言 | DEFERRED | REST 权限和查询成本模型稳定 |
| RH-M9-096 | Platform | 生产 HA、多地域、分片、PITR | DEFERRED | MVP 容量与 RPO/RTO 确定 |

## 12. 推荐执行顺序

```text
M0
 ├─> M1 ─> M2 ─> M3 ─> M4 ─> M5
 │                    └───────> M6
 └────────────────────────────> M7
                         M5/M6/M7 ─> M8
```

详细顺序：

```text
M0-001..006
-> M1-010..016
-> M2-020..026
-> M3-030..037
-> M4-040..047
-> M5-050..059
-> M6-060..067
-> M7-070..077
-> M8-080..087
```

M5 的三个 producer 可以在契约冻结后并行，但联合 E2E 必须等 Record Hub projection handler 完成。M6 diagnostic workflow 不得提前改造真实业务流程。

## 13. MVP 完成门槛

以下条件必须同时满足：

- M0-M8 全部 `DONE`；
- 三个 source projection 均来自真实业务事务 Outbox；
- Temporal 与 Conductor Binding 均有真实 engine E2E；
- duplicate、gap、ACK loss、NATS outage、Mongo outage、Dex key rotation 均有自动化证据；
- Bids 敏感字段和跨租户负向测试通过；
- 不存在外部业务状态写回或跨系统事务承诺；
- 本地 runbook 可从空环境重复搭建并完成验收。
