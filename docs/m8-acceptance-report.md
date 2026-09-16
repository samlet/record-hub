# Record Hub MVP 验收报告（M8-087）

日期：2026-09-17

## 结论

四个仓库的代码级契约、Outbox、Binding、projection、Web/BFF 安全边界和负向 gate
均可重复执行；本机已补齐 Homebrew 原生 MongoDB，并完成真实 Mongo/NATS smoke、
Mongo ACK-loss gate、API/all-mode 三源 runtime projection gate，以及监督式三 producer
relay→projection gate。MVP **尚未整体验收通过**：当前主机没有 Docker daemon，Dex
配置与 Record Hub 默认 issuer/client 不一致，NATS outage、凭据滚动轮换、真实双引擎
Binding E2E 和 Next.js grid UI 仍是遗留项。

因此本报告把“代码级 DONE”“依赖 live PARTIAL”“明确 DEFERRED”分开，不把 fake 或
单元测试当成跨进程 E2E。

## 里程碑状态

| 批次 | 状态 | 证据/限制 |
| --- | --- | --- |
| M0 | DONE | Go module、OpenAPI、envelope、配置/日志、CI 基线 |
| M1 | DONE（M1-014 DEFERRED） | Mongo/NATS/Dex 本地拓扑、OIDC verifier/JWKS、membership；Dex stable 不提供 client_credentials |
| M2 | PARTIAL | Schema server/persistence/API 与 Next.js Schema 编辑基础完成；字段级 UI 验收仍待浏览器矩阵 |
| M3 | PARTIAL | workspace/table/record/view/index server 与 Next.js grid/record 基础完成；视图/字段编辑仍待浏览器矩阵 |
| M4 | DONE | durable projection、Inbox、事务 checkpoint、gap/retry/DLQ、Operations API/UI |
| M5 | PARTIAL | 三 producer contract/outbox/relay gate、`make m5-runtime-smoke` 和 `make m5-supervised-live` 均通过；监督式 gate 默认已覆盖三业务 HTTP API→事务 Outbox→relay→projection，浏览器端投影旅程仍待真实拓扑 |
| M6 | PARTIAL | Temporal/Conductor diagnostic binding 与 client gate 通过；真实双引擎 + Record Hub 重启 E2E 待依赖 |
| M7 | 混合 | scope/payload/metrics/Dex JWKS/bounds DONE；Mongo ACK loss、Record Hub native restart 与隔离 NATS outage/recovery 已有 live 证据；三业务进程完整重启保持 PARTIAL |
| M8 | 混合 | OIDC/BFF/Operations 代码级 gate、Next.js UI 构建与 native API repository/worker smoke 完成；happy/failure/live runbook 均明确 live 限制；Web UI 浏览器矩阵仍 PARTIAL |
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

其中 `make m8-native-smoke`、`make m5-runtime-smoke`、`make m5-supervised-live`、`make m7-native-restart`、`make m7-native-nats-recovery`、M5 live gate、M7 Mongo fault gate 和 M7 NATS live gate
已通过。默认 `RECORD_HUB_M8_LOCAL_LIVE=1` 仍走 Docker；要连接本机进程请使用
`RECORD_HUB_M8_RUNTIME=native`。Dex live gate 尚未执行，因为当前运行的 Dex 是其他
项目配置。`make secret-scan` 仍依赖 Docker gitleaks 镜像。

## 推送的关键 commit/hash

| 仓库 | 关键提交（均与 upstream HEAD 一致） |
| --- | --- |
| Record Hub | `af23b54` Web OIDC Session/CSRF；`9f28dc3` 资源路由；`bceb7b6` Operations consumer/scope；`0fd5859` M8 happy gate；`a4d8ce4` M8 failure gate；`37ce631` local runbook；`67f184a` recovery/rotation runbook |
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
   草稿/发布、projection operations）；M2-026、M3-036、M3-037 的字段编辑、grid、
   tag、filter、projection freshness 真实浏览器路径尚未形成矩阵，M8-081 相应保持
   PARTIAL。
3. pending OIDC state 当前是有界一次性内存 store；多实例部署前需替换共享短期 store。
   Session secret 当前单 key，轮换会使所有浏览器 session 失效。
4. Dex 2.45.1 machine `client_credentials` 仍延期，不得以 password grant 冒充机器身份。
5. Mongo/NATS 单节点本地拓扑不代表生产 HA、PITR、TLS、跨地域或容量结论；native
   restart/recovery gate 使用 Record Hub 与隔离 NATS 实例，不等于三业务进程和依赖同时重启。

## 下一步建议

1. 在监督式 gate 中继续扩展三个业务系统的 HTTP/UI 业务变更矩阵；当前 gate 已覆盖
   Approver application、Fluxion project、Bids tender 的真实 HTTP API→事务 Outbox→relay→
   projection，浏览器 UI 和完整用户旅程仍待补齐。使用 `..._API_MODE=required` 可在本地
   依赖齐全时禁止 direct-outbox fallback。
2. 在 `web/` Next.js 基础上补字段/视图编辑和真实 Dex/Mongo/NATS/Temporal/Conductor
   监督式浏览器路径，复用本报告中的 Go auth/session/CSRF contract，把
   OWNER/EDITOR/VIEWER 矩阵提升为真实浏览器测试。
3. 按 `m8-recovery-runbook.md` 做一次原 event ID 的 GAP/DLQ 重放、ACK-loss、凭据
   滚动轮换并保存 backlog/lease/audit 证据；再将 native restart/recovery gate 扩展
   到三业务进程和依赖的受监督重启矩阵。
4. 单独评审 M1-014 machine identity 方案后，再决定 Dex stable、专用 Authorization
   Server 或 mTLS/workload identity；不要因为本地 Dex 可用而扩大其生产职责。
