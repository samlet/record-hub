# P5-201 JetStream stream/consumer HA

本批次固化 NATS JetStream 的生产 contract：file storage、多副本 stream/consumer、五个固定 stream、七个 durable pull consumer、explicit ACK、MaxDeliver、safe DLQ 和显式 replay。`tools/nats-init` 与本地 compose 继续保持单副本，明确只用于开发 smoke，不作为 HA 证据。

静态入口：`make p5-201`。它检查 stream/subject、durable consumer、ACK/MaxDeliver/DLQ/replay contract、owner outbox 边界和 Phase 5 contract。

当前状态为 `PARTIAL`：静态检查应全部 PASS；真实三节点故障切换、ACK loss、consumer restart、DLQ/replay、owner outbox 重启和 duplicate side-effect 证据尚未执行，live 状态为 `SKIPPED`。

解除条件：平台 owner 提供独立三节点 JetStream、四方 owner outbox 进程、fault injection 和不可变 evidence root，设置 `RECORD_HUB_P5_NATS_LIVE=1` 后重新执行 gate。普通本地单节点 NATS 只能证明 contract smoke。
