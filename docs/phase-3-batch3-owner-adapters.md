# Phase 3 Batch 3 owner adapter 验收记录

- 日期：2026-09-19
- 范围：P3-300～P3-307
- live 状态：P3-308 另行标记 `SKIPPED`，不以本地单测替代四进程故障矩阵。

## 已提交实现

| 仓库 | Commit | 交付 |
| --- | --- | --- |
| Approver | `3194bf8` | Flyway V32、`application.annotate` strict codec/executor、owner Inbox、durable pull consumer、result Outbox relay |
| Bids | `c39e07f` | PostgreSQL V27/SQLite AutoMigrate、`tender.annotate` strict codec/executor、owner Inbox、durable pull consumer、result Outbox relay |
| Fluxion | 既有 Batch 1 基线 | `project.annotate` 的同一 command/result 语义与共享 contract gate |
| Record Hub | `9650193` | 三份 exact policy fixture、command receipt 安全运维汇总、批次状态和 skip log |

## 共享行为边界

三 owner 的 adapter 都遵守同一条处理顺序：strict decode → operation claim → tenant/workspace/resource
和 expected version 校验 → owner 事务内追加 note 与 result Outbox → commit 后 ACK。相同 operation/hash
只 replay 原结果；相同 operation/不同 hash、非法 payload、未知 annotation、过期 version 均 fail closed。

`APPEND`/`VOID` 只保存 integration note/void 关系。它们不修改审批决定、workflow stage、派单、报价、
投标、文件、定标、合同或付款事实；这些事实仍由各 owner 的原生 workflow/数据库拥有。

## 验证

- Approver：`mvn -q -pl approver-worker -am -DskipITs test` 通过；codec unknown-field/hash/VOID 边界测试通过。
- Bids：`go test ./...` 通过；recordhub processor replay/hash/version/strict payload 测试通过。
- Record Hub：commands/app/runtime/config 测试通过；`make p3-contract-gate` 继续作为四仓库镜像门禁。
- 未执行：跨四个真实进程的 NATS outage、ACK-loss、owner restart、DLQ 和 workflow history 断言；统一归入 P3-308 重试条件。
