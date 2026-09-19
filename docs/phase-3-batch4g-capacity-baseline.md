# Phase 3 Batch 4G：容量与 recovery baseline

- 日期：2026-09-20
- 状态：P3-407 `PARTIAL`
- 入口：`make p3-capacity-baseline`

## 已交付 baseline

`verify-p3-capacity-baseline.sh` 在真实 MongoDB replica set、临时 NATS JetStream 和 Record Hub
projection 进程上发布 bounded synthetic `approver.application.summary-changed` envelopes，不写入
任何 owner business table。默认样本数为 200，可用 `RECORD_HUB_P3_CAPACITY_EVENTS` 调整（上限 10000）。

输出包含 publish/drain rate、backlog drain seconds 以及 projection latency p50/p95/p99/max。

## 尚未宣称的范围

这不是四 owner 混合业务容量结论：尚未把 Approver、Fluxion、Bids 的真实 transactional outbox、
Temporal/Conductor workflow、command/result backlog 和并发 worker 一起压测。因此结果 JSON 会把
`fourOwnerMixedWorkload` 标记为 `SKIPPED`，P3-407 在任务表中保持 `PARTIAL`。下一次需要固定数据
规模、事件率阶梯、worker 并发、硬件记录和 recovery drain SLO，再在四 owner topology 内重复测试。
