# Phase 5 native topology preflight

Phase 5 的 live gate 需要四 owner 和 Temporal/Conductor/MongoDB/NATS/Dex 的隔离运行环境。本地已有一套经过 Phase 3 验收的原生 supervisor：`scripts/verify-p3-four-owner-topology.sh`。本入口把它作为 Phase 5 的 topology preflight 复用，使用专用端口（Mongo 37018、NATS 14223、Temporal 17233、Record Hub 18081、Approver 18090、Fluxion 18092、Bids 18093）、临时 PostgreSQL database、临时 runtime/evidence 目录，并只清理自己启动的进程。

静态入口：`make p5-topology`。它检查 native toolchain、四仓库路径、P3 supervisor/fixture、隔离端口 contract、RC artifact/MinIO readiness 和 no-GA-overclaim 规则。

本机 readiness 入口：

```text
RECORD_HUB_P5_TOPOLOGY_LIVE=1 \
RECORD_HUB_P5_TOPOLOGY_EVIDENCE_ROOT=/absolute/evidence/root \
RECORD_HUB_P5_TOPOLOGY_MINIO_ENDPOINT=http://127.0.0.1:9000 \
make p5-topology-live
```

live 运行只证明四 owner 能够启动并 ready，不证明 P4-501/P5-G1～G7、故障矩阵、restore、容量、灰度、GA signoff 或 rollout。任何 readiness PASS 都必须附加到后续 live harness，不能直接解除 `BLOCKED_BY_P4` 或创建 GA candidate。
