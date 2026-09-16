# M5 三业务系统生产者验收记录

## 结论

M5-050..058 的代码、契约和确定性联合验收已完成。三个业务系统都从自己的业务事务 Outbox 产生只读 summary domain event，再由独立 relay 发布到 Record Hub JetStream；Record Hub 使用既有 summary handler 校验并进入 projection 消费管线。各仓库分别提交，未制造跨仓库原子提交。

本批未改变 Temporal workflow 的业务语义，也没有从 Record Hub 向业务系统写回状态。RH-M5-059 的监督式 gate 已经可以优先走三个业务 HTTP API 产生事务 Outbox，再验证真实 MongoDB/JetStream 投影；浏览器 UI 入口仍不在范围内，因此任务继续标记为 `PARTIAL`。

## 契约与提交

| 生产者 | 契约镜像 | Outbox/relay | 远端提交 |
| --- | --- | --- | --- |
| Approver | `application-summary-v1`；`sha256:c7c269943d0d84c090a00c1c624fa5f08c1e994df82016c8dd26ba3d16f621ae` | `APPLICATION_SUMMARY_CHANGED`；`events.approver.application.summary-changed.v1` | `fa1cb54`, `75a682b`, `4c5cec9`, `4931941`, `188b470`, `71e7f4f` |
| Fluxion | `project-summary-v1`；`sha256:311dbb7d1c9b3841cacd0ada034fba1077c494841b24baaca18a19ffe196a398` | `PROJECT_SUMMARY_CHANGED`；`events.fluxion.project.summary-changed.v1` | `4fb5cfb`, `6ab93e1`, `5f872de` |
| Bids | `tender-summary-v1`；`sha256:024524fa5b60f8a8deac700bf19745c121f450439532ba22d7ab0f882cad1fff` | `TENDER_SUMMARY_CHANGED`；`events.bids.tender.summary-changed.v1` | `c88f719`, `1c5792b`, `2282766`, `21599a1` |

上述提交均已推送到各自远端（Approver/Fluxion/Bids 为 Gitee；Record Hub 为 GitHub）。Record Hub 的权威 manifest hash 修正提交为 `7592453`。

## 实现验收

- Approver 在创建、保存、提交、补正、挂接 workflow 等摘要变化路径中，和业务状态在同一事务写入 `integration_outbox`；稳定 `summary_version` 和 `eventId`，摘要 payload 只含安全字段。普通审批 relay 与 summary relay 使用 typed claim 分区，避免互相领取消息。
- Fluxion 在 Project 状态、终态和 run ID 变更时，在同一 Exposed 事务写入 `projects`、`project_events` 和 `record_hub_outbox`，不轮询 Temporal visibility。relay 实现 lease、retry、dead，并在 API/Worker 启动。
- Bids 复用现有 `outbox_events` 和 lease 机制，新增 `TENDER_SUMMARY_CHANGED` 专用事件类型；Conductor/Finance command 不会被 summary relay 领取。业务服务和 worker 的 tender 状态转换均接入事务摘要事件。
- 三个 relay 均在 JetStream publish ACK 后标记 SENT；ACK 丢失会保留同一 outbox/event ID 重发，并通过 `Msg-Id` 支持幂等发布。
- 三种 summary schema 均使用 Draft 2020-12、显式 allowlist 和 fixture；contract mirror 检查拒绝联系人、报价、投标金额及文件 URL 等敏感字段。
- 三个 producer 均支持通过独立环境变量向新 summary envelope 写入可选的
  `metadata.workspaceId`；未配置时不改变已有 envelope，Record Hub 仍可用 tenant map
  或显式 fallback 兼容历史事件。

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

本机 native 依赖上的监督式联合 gate：

```bash
RECORD_HUB_M5_SUPERVISED_LIVE=1 make m5-supervised-live
```

该 gate 构建并启动 Approver API、Fluxion API、Bids worker/API 和 Record Hub all-mode；为
Approver/Fluxion 创建临时 PostgreSQL 数据库，为 Bids 创建临时 SQLite 数据库。默认
`RECORD_HUB_M5_SUPERVISED_API_MODE=auto` 会先通过业务 API：Approver 临时发布表单/流程定义
并创建 application draft，Fluxion 登录后创建 customer/project，Bids 登录后完成 project
审批并创建 tender。每个 API 事务产生的真实 source Outbox 都由 relay 发布到 JetStream，
Record Hub durable projection 最终写入三条 `CURRENT` Mongo record。若本地 API 依赖不可用，
auto 模式才回退到受控 Outbox seed；`..._API_MODE=required` 会将回退视为失败，
`..._API_MODE=off` 可显式复现旧的 direct-outbox gate。成功后脚本会终止本次启动的进程、
删除生成的临时数据库，不触碰既有业务库。

```text
M5 supervised source outbox -> relay -> projection: PASS
M5-059 supervised live producer -> relay -> projection gate: PASS
M5 supervised: Approver application API created ...
M5 supervised: Fluxion project API created ...
M5 supervised: Bids tender API created ...
```

本机 native Mongo 使用 `rs0`、无认证开发 URI；Docker 拓扑仍使用文档中的
`record-hub-rs`、keyfile 和应用用户。M5-059 仍保持 `PARTIAL`：监督式 gate 已验证三个
真实 producer relay 进程到 durable projection consumer 的联合链路，并在 native 拓扑中覆盖
了三个业务 HTTP API 的事务触发；浏览器 UI 入口、业务 API 的完整用户旅程和多实例认证拓扑
仍待后续批次。
M6 的 snapshot/binding、真实 Temporal/Conductor engine E2E，以及 M7 的 outage/rotation
故障注入不属于本批范围。
