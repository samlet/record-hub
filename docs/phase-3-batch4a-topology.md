# Phase 3 Batch 4A：隔离拓扑与确定性 fixture

- 日期：2026-09-19
- 状态：P3-400/P3-401 `DONE`
- 任务：[phase-3-task-breakdown.md](phase-3-task-breakdown.md)
- 验收：[phase-3-acceptance-plan.md](phase-3-acceptance-plan.md)

## 目标

Batch 4A 只解决 live gate 的启动和输入准备，不宣称 command/approval 业务 E2E 已完成。它提供一套
不依赖 Docker 的本机 supervisor，可以在用户已有服务之外，以专用端口、临时数据库和临时目录启动四个真实
业务系统及其 workflow 基础设施。

## 入口

```text
make p3-fixtures
RECORD_HUB_P3_FOUR_OWNER_LIVE=1 make p3-four-owner-topology
```

默认路径和依赖：

- `APPROVER_ROOT=/Users/xiaofeiwu/apps/approver`
- `FLUXION_ROOT=/Users/xiaofeiwu/portals/fluxion`
- `BIDS_ROOT=/Users/xiaofeiwu/apps/bids`
- 原生 `mongod` replica set、`nats-server -js`、Dex、workload issuer、Temporal dev server、Conductor
- 临时 PostgreSQL database；Bids API 使用本机 MinIO 的 health endpoint

所有端口均可用 `RECORD_HUB_P3_*` 覆盖。脚本先检查二进制、仓库路径、MinIO 和端口；端口占用时直接
`SKIPPED`，不会杀死未知 PID。它只登记并清理自己启动的进程和自己创建的 PostgreSQL database；失败时
保留 evidence/runtime 路径用于诊断，成功时删除临时 runtime。

## Fixture contract

`deploy/local/p3/p3-fixture-spec.json` 是无 secret 的确定性输入，声明三个 owner 的 subject、resource
type、action、purpose、migration、tenant/workspace 和 Temporal/Conductor 拓扑。bootstrap 脚本会校验三份
exact policy fixture，然后原子生成：

```text
topology.json
command-policies.json
owner-fixtures.json
schema-plan.json
record-hub-command-policies.env.json
manifest.json
```

输出目录默认是 `.runtime/p3/fixtures`，可由 `RECORD_HUB_P3_FIXTURE_DIR` 指定。文件没有时间戳、随机 ID
或 secret；manifest 对五个输出逐一计算 SHA-256，因此重复运行内容和 hash 稳定。脚本不直接写业务表，
schema/mapping 的落地仍由真实应用 migration 和后续 live gate 完成。

## Live evidence

2026-09-19 在 Darwin arm64 原生环境执行：

```text
RECORD_HUB_P3_FOUR_OWNER_LIVE=1 ./scripts/verify-p3-four-owner-topology.sh
```

结果：`PASS`，耗时约 39 秒。证据目录的 `manifest.json` 记录：

- Mongo `37018`、NATS `14223/18223`、Dex `15566`、workload issuer `15557`；
- Temporal `17233`、Conductor `18080`、Record Hub `18081`；
- Approver API/worker `18090/18091`、Fluxion API `18092`、Bids API `18093`；
- Record Hub `44eef02`、Approver `3194bf8`、Fluxion `1321285`、Bids `c39e07f`；
- fixture manifest 和每个进程日志。

本批只证明四应用及其依赖可以在隔离拓扑中同时启动并 ready。P3-308 的 replay/hash-conflict/version/
rollback/ACK-loss/outage 矩阵仍需后续业务操作脚本；P3-402..409 仍未通过。
