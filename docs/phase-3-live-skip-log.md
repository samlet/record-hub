# Phase 3 live gate skip log

## P3-110 — Fluxion command E2E

- 初始状态：`SKIPPED`（2026-09-18）；已由 2026-09-19 的核心 live slice 取代为 `PARTIAL`。
- 已通过：`make p3-fluxion-command-live` 使用隔离 Mongo replica set、NATS JetStream、Temporal、临时
  PostgreSQL、Record Hub `all` 和 Fluxion API/worker；项目通过 Fluxion API 建立，真实验证 APPEND、VOID、
  相同 operation replay、hash conflict、过期 version 拒绝、Inbox/Result Outbox、summary version +2、
  Record Hub projection source version 收敛。
- 证据：脚本每次运行生成 `manifest.json`、脱敏 `config-redacted.json`、`results.json`、日志和数据库断言；
  最近一次核心运行的结果为 PASS（Darwin arm64，Record Hub `942092b` 基线、Fluxion `7811ddd`，运行约 21 秒）。
- 故障矩阵入口已实现：`make p3-fluxion-command-faults` 会在同一隔离拓扑中启动第二个 Fluxion worker，
  使用 Fluxion 测试故障点验证 commit-before-ACK 重放，并暂停 Record Hub result consumer 验证 durable
  result 收敛；每次运行继续生成相同格式的 evidence directory。
- NATS outage/backlog recovery、result poison DLQ、跨系统同时重启和两个 Record Hub result worker 竞争均已由故障矩阵覆盖。
- 尚未覆盖：更细粒度的 owner transaction rollback/发布前崩溃注入；这些保持为后续批次的 `TODO`，
  不能据此宣称 Phase 3 全部 Gate 通过。
- 不以 fake server、内存仓库或直接写业务库替代 live Gate；脚本只使用数据库读查询做断言。
- 后续重试条件：在本 gate 基础上加入故障注入与重启控制，继续使用
  `deploy/local/p3/fluxion-project-annotate-policy.json` 和 tenant/workspace 精确映射，保存同一格式的
  evidence directory。

## P3-212/P3-213 — Fluxion ↔ Approver approval pilot E2E

- 当前状态：`SKIPPED`（2026-09-19）。本批次已完成 request/result contract、Fluxion request/outbox/Inbox、Approver
  `FLUXION` handler/materializer/connector/result delivery、Fluxion result Inbox/Temporal update Outbox 和默认关闭
  fallback，但本机尚未准备一套可复现的 published Approver process、tenant/org/requester mapping、Dex workload
  token、真实 Temporal child workflow 与 API/worker 四进程隔离拓扑。
- 已验证：Fluxion Gradle 全量测试通过；Approver Maven 全量测试通过；Fluxion V1→V10 临时 PostgreSQL migration
  和 Approver V31 静态迁移构建通过；`make p3-contract-gate` 通过；所有代码提交已推送，见 task breakdown 的 SHA。
- 不以 fake Approver、内存 store、手工改库或直接 Temporal CLI 代替 live Gate。P3-212 的五类终态和 P3-213 的
  timeout/late/restart/reconciliation 必须在 published process + Dex/service principal + real Temporal/NATS
  拓扑准备好后重试；当前跳过不表示业务 E2E 已通过。

## P3-214 — approval association safe projection

- 当前状态：`DONE`（2026-09-19，bounded contract/projection gate）。Record Hub `727b1ba` 新增严格
  `approver.dispatch-approval.summary-changed` v1 safe summary，使用独立 approval projection table/schema，
  只保留 Application/Project/workflow stable refs、status、proposal hash、generation、decision version 和
  freshness；unknown fields 及客户、候选、人员、地址、金额、文件字段均被拒绝。
- 已验证：`go test ./...` 全量通过；summary handler、registry、projector 和 forbidden-field boundary tests 通过。
- 说明：Approver/Fluxion 真实 approval summary producer 与四进程 live delivery 仍属于 P3-212/P3-213/P3-402
  的 live gate；本条不将 bounded projection unit gate 误报为跨系统 E2E。

## P3-308 — three owner command live matrix

- 当前状态：`SKIPPED`（2026-09-19）。Approver `application.annotate`、Bids `tender.annotate` 和既有
  Fluxion `project.annotate` 的 owner adapter、Inbox、result Outbox 与 durable runner 已完成静态实现和仓库级
  测试，但本机没有一套可复现的四应用隔离拓扑同时启动 Record Hub、Approver、Fluxion、Bids，并连接同一组
  NATS JetStream、Dex workload issuer、PostgreSQL、Temporal/Conductor。
- 已验证：Approver `3194bf8` Maven worker/application 测试、Bids `c39e07f` `go test ./...`、Record Hub
  command receipt 运维汇总测试和 `make p3-contract-gate`。这些证据不等价于跨进程 ACK-loss 或 outage 通过。
- 不以 fake server、内存 repository、手工改库或直接发布 NATS 消息替代 live Gate。待 P3-400/401 准备好四
  应用进程、Dex workload principals、tenant/workspace exact policy 和 PostgreSQL schema 后，按 owner 逐个
  注入：相同 operation replay、hash conflict、version reject、transaction rollback、commit-before-ACK
  crash、result publish/mark-sent crash、NATS outage/backlog、owner restart 和 DLQ。
- 重试条件：生成独立 `manifest.json`、脱敏配置、每进程日志、metrics 和 DB assertions；任一 owner outage
  不得阻塞其他 durable consumer，恢复后 receipt/result 只收敛一次且无重复 note 副作用。

## P3-400/P3-401 — isolated four-owner topology and fixture bootstrap

- 当前状态：`DONE`（2026-09-19）。`scripts/verify-p3-four-owner-topology.sh` 已在本机使用原生 MongoDB、
  NATS JetStream、Dex、workload issuer、Temporal、Conductor、Record Hub、Approver、Fluxion 和 Bids 的
  隔离端口/临时 PostgreSQL database 启动并通过 readiness；退出只清理脚本登记的 PID 和 database。
- 证据：live PASS 的 `manifest.json` 记录 Darwin arm64、四仓库 commit、端口和 fixture manifest；每个
  进程有独立日志。`scripts/bootstrap-p3-fixtures.sh` 生成 topology、owner、policy、schema 五份稳定
  文件及 SHA-256 manifest，重复运行结果一致且不含 secret。
- 本条不等价于 P3-308 或 P3-402 的业务 E2E。后续 gate 必须在该隔离入口上执行真实 command/approval
  操作、故障注入和 DB 最终断言；不能只因 topology ready 就把 owner matrix 标记为通过。
