# Record Hub MVP 验收报告（M8-087）

日期：2026-09-17

## 结论

四个仓库的代码级契约、Outbox、Binding、projection、Web/BFF 安全边界和负向 gate
均可重复执行；本机已完成真实 Mongo/NATS smoke、Mongo ACK-loss gate、API/all-mode
三源 runtime projection gate，以及禁止 fallback 的监督式三 producer 业务 HTTP API →
事务 Outbox → relay → projection gate。Schema catalog、六类字段渲染和版本化 View
编辑也已完成。

MVP 实现批次至此 **有条件收口**，但未达到“无跳过的整体验收通过”：当前主机没有
Docker daemon；5556 端口运行的是其他项目的 Dex，其 issuer/client/redirect 与 Record Hub
不兼容；Dex 2.45.1 也不提供 `client_credentials`。因此真实 Dex 浏览器矩阵、带 machine
token 的双引擎 live E2E、四系统共同 outage/restart 和 credential rotation 被显式标记为
`SKIPPED`，不能计作 PASS。

因此本报告把“代码级 DONE”“依赖 live PARTIAL”“明确 DEFERRED”分开，不把 fake 或
单元测试当成跨进程 E2E。

## 里程碑状态

| 批次 | 状态 | 证据/限制 |
| --- | --- | --- |
| M0 | DONE | Go module、OpenAPI、envelope、配置/日志、CI 基线 |
| M1 | DONE（M1-014 DEFERRED） | Mongo/NATS/Dex 本地拓扑、OIDC verifier/JWKS、membership；Dex stable 不提供 client_credentials |
| M2 | DONE | Schema server/persistence/API、workspace catalog、六类字段编辑、错误位置、ETag draft/publish 完成 |
| M3 | DONE + SKIPPED | typed grid、record/tag、版本化 View 编辑完成；真实投影登录浏览器矩阵 SKIPPED |
| M4 | DONE | durable projection、Inbox、事务 checkpoint、gap/retry/DLQ、Operations API/UI |
| M5 | DONE | required API mode 下三个真实业务 HTTP API→事务 Outbox→relay→projection 联合 E2E 通过，无 direct-outbox fallback |
| M6 | DONE + SKIPPED | Temporal/Conductor contract/retry gate 通过；带 machine token 的双引擎 live/restart E2E SKIPPED |
| M7 | DONE + SKIPPED | scope/payload/metrics/Mongo ACK loss、Record Hub restart、隔离 NATS outage/recovery 通过；四系统联合 outage/restart SKIPPED |
| M8 | DONE + SKIPPED | Web 功能、build、deterministic/native gates 与 runbook 完成；Dex 浏览器、bad-token 和 credential rotation live 矩阵 SKIPPED |
| M9 | DEFERRED | Command Gateway、审批迁移、Storage Gateway、Functions、Presence、GraphQL、生产 HA |

## 可重复证据

在 `/Users/xiaofeiwu/apps/record-hub` 执行：

```bash
make check
make m8-happy-path
make m8-failure-path
make m8-local-smoke
```

结果：`make check`、M8-083 happy-path、M8-084 failure-path 和 local default smoke
均通过。M8 gates 同时运行四仓库测试；Approver 的 ACK-loss 测试会按设计输出一条
WARN/异常栈，但测试结果为通过。

已提供的 live 命令与当前证据：

```bash
make m8-native-smoke
make m5-runtime-smoke
RECORD_HUB_M5_SUPERVISED_LIVE=1 make m5-supervised-live
make m7-native-restart
make m7-native-nats-recovery
RECORD_HUB_M5_LIVE=1 RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true' ./scripts/verify-m5-producers.sh
RECORD_HUB_M7_MONGO_LIVE=1 RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true' ./scripts/verify-m7-mongo-faults.sh
RECORD_HUB_M7_NATS_LIVE=1 RECORD_HUB_NATS_URL='nats://127.0.0.1:4222' ./scripts/verify-m7-nats-recovery.sh
RECORD_HUB_M8_LOCAL_LIVE=1 ./scripts/verify-m8-local.sh
make dex-smoke
```

其中 `make m8-native-smoke`、required API mode 的 `make m5-supervised-live`、
`make m7-native-restart`、`make m7-native-nats-recovery` 以及
`./scripts/verify-m6-bindings.sh` 已通过。默认 `RECORD_HUB_M8_LOCAL_LIVE=1` 仍走 Docker；要连接本机进程请使用
`RECORD_HUB_M8_RUNTIME=native`。Dex live gate 尚未执行，因为当前运行的 Dex 是其他
项目配置。`make secret-scan` 仍依赖 Docker gitleaks 镜像。

## 推送的关键 commit/hash

| 仓库 | 关键提交（均与 upstream HEAD 一致） |
| --- | --- |
| Record Hub | `af23b54` Web OIDC Session/CSRF；`9f28dc3` 资源路由；`bceb7b6` Operations consumer/scope；`0fd5859` M8 happy gate；`a4d8ce4` M8 failure gate；`37ce631` local runbook；`67f184a` recovery/rotation runbook；`94bf09b` schema-aware cells；`cd0927d` Schema catalog；`0d8c2d6` versioned View editing |
| Approver | `fa1cb54` ApplicationSummary contract；`75a682b` summary Outbox/Record Hub relay；`4c5cec9` workspace metadata；`4931941` dispatcher constructor；`188b470` local security filter wiring；`71e7f4f` header filter order fix |
| Fluxion | `4fb5cfb` ProjectSummary contract；`6ab93e1` summary relay；`6d496b0` Temporal diagnostic binding；`5f872de` workspace metadata |
| Bids | `c88f719` TenderSummary contract；`1c5792b` summary relay；`2282766` tenant correction；`616e7fd` Conductor diagnostic binding；`21599a1` workspace metadata |

列出的功能提交均已推送；四个仓库在本报告生成时工作树均 clean，且
`HEAD == @{upstream}`（本报告提交本身不计入上面的功能 hash 列表）。

## 已知遗留风险

1. `server/internal/app` 现在会在配置 Mongo/NATS URI 时实例化 Mongo repositories、
   records/schema/binding/Operations handlers 和三个 durable projection consumers；
   未配置 URI 时仍保留 dependency-free contract boundary。Approver、Fluxion、Bids 的
   新 summary 事件可通过各自环境变量写入 `metadata.workspaceId`；未配置的历史事件
   仍需受校验的 tenant→workspace map 或显式 fallback，不能隐式猜 scope。
2. `web/` Next.js 控制台已落地最小闭环（登录入口、workspace/table/record、Schema
   catalog/草稿/发布、六类 Schema 字段编辑、typed grid、View CAS 编辑、projection
   operations）；M3-037/M8-081 的真实 Dex 登录后浏览器矩阵为 `SKIPPED`。
3. pending OIDC state 当前是有界一次性内存 store；多实例部署前需替换共享短期 store。
   Session secret 当前单 key，轮换会使所有浏览器 session 失效。
4. Dex 2.45.1 machine `client_credentials` 仍延期，不得以 password grant 冒充机器身份。
5. Mongo/NATS 单节点本地拓扑不代表生产 HA、PITR、TLS、跨地域或容量结论；native
   restart/recovery gate 使用 Record Hub 与隔离 NATS 实例，不等于三业务进程和依赖同时重启。

## 下一步建议

1. 先完成 machine identity ADR，提供 Record Hub 专用 issuer/client；用户登录仍可使用
   Dex，服务身份不得以 password grant 模拟。
2. 建立不会占用其他项目端口的受监督测试拓扑，再把 Dex 登录、多角色、Temporal、
   Conductor、三个 producer 和 Record Hub 串为浏览器/API 联合矩阵。
3. 按 `m8-recovery-runbook.md` 做一次原 event ID 的 GAP/DLQ 重放、ACK-loss、凭据
   滚动轮换并保存 backlog/lease/audit 证据；再将 native restart/recovery gate 扩展
   到三业务进程和依赖的受监督重启矩阵。
4. 下一期以“可运维集成平台”而非 Supabase 全量复制为目标；具体范围见
   [下一期方案评估](phase-2-evaluation.md)与[下一期需求设计](phase-2-requirements.md)。
