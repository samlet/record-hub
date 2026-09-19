# Phase 3 Batch 4C：NATS outage / backlog / recovery

- 日期：2026-09-19
- 状态：P3-403 `DONE`
- 依赖：[phase-3-batch4a-topology.md](phase-3-batch4a-topology.md)、[phase-3-batch4b-workflow-e2e.md](phase-3-batch4b-workflow-e2e.md)
- 任务：[phase-3-task-breakdown.md](phase-3-task-breakdown.md)

## 目标

在同一套原生四 owner 隔离 topology 中，验证 NATS 完全不可用时真实 owner 事实不会丢失：Fluxion
Temporal project workflow 仍通过 API/数据库事务生成 summary outbox；NATS 恢复并复用原 JetStream
文件存储后，dispatcher 能够重试发送，Record Hub 只应用一次每个事件，最终投影与最高 owner version
一致。

## 入口

```text
make p3-nats-outage-recovery
RECORD_HUB_P3_NATS_RECOVERY_LIVE=1 ./scripts/verify-p3-nats-outage-recovery.sh
```

未设置 live flag 或缺少 `curl/go/jq/lsof/mongosh/nats/nats-server/psql/uuidgen` 时安全返回 `SKIPPED`。
真正运行由 `verify-p3-four-owner-topology.sh` 启动 MongoDB、NATS JetStream、Dex、workload issuer、
Temporal、Conductor、Record Hub、Approver、Fluxion 和 Bids；ready hook 是独立的 process boundary，
避免递归启动 topology。

## 验收路径

1. 通过 Fluxion 登录和 customer API 准备真实业务输入，然后停止 supervisor 自己启动的 NATS PID。
2. 在 NATS 停机期间调用真实 `POST /api/projects`，不直接插入业务表；Temporal worker 和 Fluxion
   repository 将 project summary events 与业务写入保持在同一事务语义内。
3. 轮询 Fluxion PostgreSQL `record_hub_outbox`，要求该项目至少一条 `PENDING/PROCESSING`、attempts
   已增长，并保存 outage TSV 作为 backlog evidence。
4. 用同一 `runtime_root/nats` JetStream store 重启 NATS，运行 `tools/nats-init` 验证 stream 和 durable
   consumer 配置，再等待 owner outbox 全部 `SENT`。
5. 查询 Mongo `inbox_events` 和 `records`：所有该项目的 Fluxion summary event 必须 `APPLIED`，当前
   record `source.version` 必须等于 outbox payload 中最高 `aggregateVersion`。唯一 Inbox 约束和单条
   current record 共同证明无丢失/重复副作用。

## 证据与边界

每次运行输出 topology manifest 与：

```text
nats-outage/nats-down.json
nats-outage/outbox-during-outage.tsv
nats-outage/domain-events-after-recovery.json
nats-outage/fluxion-consumer-after-recovery.json
nats-outage/domain-events-final.json
nats-outage/fluxion-consumer-final.json
nats-outage-recovery.json
db-assertions/p3-403-nats-outage.json
```

P3-403 不覆盖每个 commit/ACK 边界的进程崩溃、JWKS/secret rotation、Mongo/PostgreSQL/JetStream 备份
恢复、容量基线或升级回滚；这些由 P3-404..P3-408 继续验收。
