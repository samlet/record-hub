# M5 三业务系统生产者验收记录

## 结论

M5-050..058 的代码、契约和确定性联合验收已完成。三个业务系统都从自己的业务事务 Outbox 产生只读 summary domain event，再由独立 relay 发布到 Record Hub JetStream；Record Hub 使用既有 summary handler 校验并进入 projection 消费管线。各仓库分别提交，未制造跨仓库原子提交。

本批未改变 Temporal workflow 的业务语义，也没有从 Record Hub 向业务系统写回状态。RH-M5-059 的真实 MongoDB/JetStream 只读表联合 E2E 需要在本地依赖启动后显式执行，当前环境因此标记为 `PARTIAL`，而不是虚报为已完成。

## 契约与提交

| 生产者 | 契约镜像 | Outbox/relay | 远端提交 |
| --- | --- | --- | --- |
| Approver | `application-summary-v1`；`sha256:c7c269943d0d84c090a00c1c624fa5f08c1e994df82016c8dd26ba3d16f621ae` | `APPLICATION_SUMMARY_CHANGED`；`events.approver.application.summary-changed.v1` | `fa1cb54`, `75a682b` |
| Fluxion | `project-summary-v1`；`sha256:311dbb7d1c9b3841cacd0ada034fba1077c494841b24baaca18a19ffe196a398` | `PROJECT_SUMMARY_CHANGED`；`events.fluxion.project.summary-changed.v1` | `4fb5cfb`, `6ab93e1` |
| Bids | `tender-summary-v1`；`sha256:024524fa5b60f8a8deac700bf19745c121f450439532ba22d7ab0f882cad1fff` | `TENDER_SUMMARY_CHANGED`；`events.bids.tender.summary-changed.v1` | `c88f719`, `1c5792b`, `2282766` |

上述提交均已推送到各自远端（Approver/Fluxion/Bids 为 Gitee；Record Hub 为 GitHub）。Record Hub 的权威 manifest hash 修正提交为 `7592453`。

## 实现验收

- Approver 在创建、保存、提交、补正、挂接 workflow 等摘要变化路径中，和业务状态在同一事务写入 `integration_outbox`；稳定 `summary_version` 和 `eventId`，摘要 payload 只含安全字段。普通审批 relay 与 summary relay 使用 typed claim 分区，避免互相领取消息。
- Fluxion 在 Project 状态、终态和 run ID 变更时，在同一 Exposed 事务写入 `projects`、`project_events` 和 `record_hub_outbox`，不轮询 Temporal visibility。relay 实现 lease、retry、dead，并在 API/Worker 启动。
- Bids 复用现有 `outbox_events` 和 lease 机制，新增 `TENDER_SUMMARY_CHANGED` 专用事件类型；Conductor/Finance command 不会被 summary relay 领取。业务服务和 worker 的 tender 状态转换均接入事务摘要事件。
- 三个 relay 均在 JetStream publish ACK 后标记 SENT；ACK 丢失会保留同一 outbox/event ID 重发，并通过 `Msg-Id` 支持幂等发布。
- 三种 summary schema 均使用 Draft 2020-12、显式 allowlist 和 fixture；contract mirror 检查拒绝联系人、报价、投标金额及文件 URL 等敏感字段。

## 一键验收

从 Record Hub 根目录执行：

```bash
./scripts/verify-m5-producers.sh
```

该脚本完成：

1. 三份 schema/fixture 字节级镜像和 manifest SHA-256 校验；
2. Record Hub 三种 summary handler 的 producer envelope 测试（确定性跨语言 gate）；
3. Approver、Fluxion、Bids 各自的契约/relay 定向测试；
4. 可选的 live MongoDB/JetStream smoke。

本次确定性验收结果：

```text
M5 contract mirrors: PASS
Record Hub summary contract/handler tests: PASS
Approver contract tests: PASS
Fluxion ProjectSummary contract tests: PASS
Bids outbox/recordhub tests: PASS
M5 producer acceptance: PASS
```

## Live smoke 与遗留项

当前开发环境通过 Homebrew 原生运行 NATS 和 MongoDB。本批已执行真实驱动 smoke：

```text
NATS topology ready: DOMAIN_EVENTS DEAD_LETTERS and projection consumers
mongo smoke passed: transaction unique-index cas change-stream
```

可重复执行完整 gate：

```bash
RECORD_HUB_M5_LIVE=1 ./scripts/verify-m5-producers.sh
```

本机 native Mongo 使用 `rs0`、无认证开发 URI；Docker 拓扑仍使用文档中的
`record-hub-rs`、keyfile 和应用用户。M5-059 仍保持 `PARTIAL`，因为当前 gate 验证了
真实 Mongo/JetStream 能力和 producer contract，但完整的三 producer → durable
projection consumer → Mongo 表联合链路仍需 Record Hub runtime wiring 和真实业务进程。
M6 的 snapshot/binding、真实 Temporal/Conductor engine E2E，以及 M7 的 outage/rotation
故障注入不属于本批范围。
