# Record Hub MVP 验收报告（M8-087）

日期：2026-09-16

## 结论

四个仓库的代码级契约、Outbox、Binding、projection、Web/BFF 安全边界和负向 gate
均可重复执行；所有已完成批次均已提交并推送到各自 upstream。MVP **尚未整体
验收通过**：当前主机没有 Docker daemon，MongoDB/NATS/Dex 真实故障与滚动轮换未
执行；Record Hub 的 Next.js grid UI 和 API 的真实 repository wiring 仍是遗留项。

因此本报告把“代码级 DONE”“依赖 live PARTIAL”“明确 DEFERRED”分开，不把 fake 或
单元测试当成跨进程 E2E。

## 里程碑状态

| 批次 | 状态 | 证据/限制 |
| --- | --- | --- |
| M0 | DONE | Go module、OpenAPI、envelope、配置/日志、CI 基线 |
| M1 | DONE（M1-014 DEFERRED） | Mongo/NATS/Dex 本地拓扑、OIDC verifier/JWKS、membership；Dex stable 不提供 client_credentials |
| M2 | PARTIAL | Schema server/persistence/API 完成；Schema Web UI（M2-026）仍 TODO |
| M3 | PARTIAL | workspace/table/record/view/index server 完成；grid/record UI（M3-036/037）仍 TODO |
| M4 | DONE | durable projection、Inbox、事务 checkpoint、gap/retry/DLQ、Operations API/UI |
| M5 | PARTIAL | 三 producer contract/outbox/relay gate 通过；真实 Mongo/JetStream 联合表 E2E 待依赖 |
| M6 | PARTIAL | Temporal/Conductor diagnostic binding 与 client gate 通过；真实双引擎 + Record Hub 重启 E2E 待依赖 |
| M7 | 混合 | scope/payload/metrics/Dex JWKS/bounds DONE；Mongo ACK loss、NATS outage、完整分进程重启保持 PARTIAL |
| M8 | 混合 | OIDC/BFF/Operations 代码级 gate 完成；happy/failure/live runbook 均明确 live 限制；Web UI 浏览器矩阵仍 PARTIAL |
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

已提供但在当前机器不能通过的 live 命令：

```bash
RECORD_HUB_M8_LOCAL_LIVE=1 ./scripts/verify-m8-local.sh
make mongo-smoke
make nats-smoke
make dex-smoke
```

原因是 Docker socket `~/.docker/run/docker.sock` 不存在。`make secret-scan`、M7
Mongo/NATS live gate 因同一原因保持 skipped/failed，不纳入通过证据。

## 推送的关键 commit/hash

| 仓库 | 关键提交（均与 upstream HEAD 一致） |
| --- | --- |
| Record Hub | `af23b54` Web OIDC Session/CSRF；`9f28dc3` 资源路由；`bceb7b6` Operations consumer/scope；`0fd5859` M8 happy gate；`a4d8ce4` M8 failure gate；`37ce631` local runbook；`67f184a` recovery/rotation runbook |
| Approver | `fa1cb54` ApplicationSummary contract；`75a682b` summary Outbox/Record Hub relay |
| Fluxion | `4fb5cfb` ProjectSummary contract；`6ab93e1` summary relay；`6d496b0` Temporal diagnostic binding |
| Bids | `c88f719` TenderSummary contract；`1c5792b` summary relay；`2282766` tenant correction；`616e7fd` Conductor diagnostic binding |

列出的功能提交均已推送；四个仓库在本报告生成时工作树均 clean，且
`HEAD == @{upstream}`（本报告提交本身不计入上面的功能 hash 列表）。

## 已知遗留风险

1. `server/internal/web` 已提供可挂载的 Go BFF auth/resource/Operations boundary，
   但 `server/internal/app` 尚未实例化 Mongo repositories、projection consumers 和
   records/schema service；因此不能把当前 API 进程视为完整生产数据平面。
2. `web/` Next.js 项目尚未落地，M2-026、M3-036、M3-037 的字段编辑、grid、tag、
   filter、projection freshness 浏览器路径未形成真实 UI 矩阵；M8-081 相应保持
   PARTIAL。
3. pending OIDC state 当前是有界一次性内存 store；多实例部署前需替换共享短期 store。
   Session secret 当前单 key，轮换会使所有浏览器 session 失效。
4. Dex 2.45.1 machine `client_credentials` 仍延期，不得以 password grant 冒充机器身份。
5. Mongo/NATS 单节点本地拓扑不代表生产 HA、PITR、TLS、跨地域或容量结论。

## 下一步建议

1. 先完成 `web/` Next.js BFF/grid，复用本报告中的 Go auth/session/CSRF contract，
   把 OWNER/EDITOR/VIEWER 矩阵提升为真实浏览器测试。
2. 接入真实 Mongo/NATS repositories 与 projection worker wiring，再按
   `m8-local-runbook.md` 启动四项依赖，执行 M5/M6/M7/M8 live gate。
3. 按 `m8-recovery-runbook.md` 做一次原 event ID 的 GAP/DLQ 重放、ACK-loss、凭据
   滚动轮换并保存 backlog/lease/audit 证据。
4. 单独评审 M1-014 machine identity 方案后，再决定 Dex stable、专用 Authorization
   Server 或 mTLS/workload identity；不要因为本地 Dex 可用而扩大其生产职责。
