# Phase 3 Batch 4B：真实 Workflow Binding E2E

- 日期：2026-09-19
- 状态：P3-402 `DONE`
- 依赖：[phase-3-batch4a-topology.md](phase-3-batch4a-topology.md)
- 任务：[phase-3-task-breakdown.md](phase-3-task-breakdown.md)
- 验收：[phase-3-acceptance-plan.md](phase-3-acceptance-plan.md)

## 目标

在 Batch 4A 的隔离四 owner topology 内，使用真实 Fluxion Temporal worker、Bids Conductor worker 和
Record Hub Mongo binding API，验证 workflow 节点引用 owner summary record 时能够生成可重放的安全 snapshot。
该 gate 同时覆盖一个真实 owner command 和一个真实 Bids 人工审批路径，但不把 P3-308 的完整故障矩阵或
P3-403..409 的恢复/容量/升级演练提前宣称为通过。

## 入口和证据

```text
make p3-workflow-e2e
RECORD_HUB_P3_WORKFLOW_E2E_LIVE=1 ./scripts/verify-p3-workflow-e2e.sh
```

脚本通过 `p3-workflow-e2e-hook.sh` 作为明确的 supervisor process boundary，避免 ready hook 递归启动
topology。未设置 live flag 时安全返回 `SKIPPED`；缺少 `conductor/curl/jq/mongosh/psql/temporal/uuidgen`
任一工具时也只返回 `SKIPPED`。

每次 live 运行的 evidence 位于 topology 临时目录，关键文件包括：

```text
workflow-e2e/fluxion-command-final.json
workflow-e2e/temporal-start.json
workflow-e2e/temporal-result.json
workflow-e2e/temporal-history.json
workflow-e2e/temporal-snapshot-db.json
workflow-e2e/bids-project-approval.json
workflow-e2e/bids-conductor-approval.json
workflow-e2e/conductor-result.json
workflow-e2e.json
db-assertions/p3-402-workflow.json
```

## 验收路径

1. 通过 Fluxion API 创建真实 customer/project，等待 summary version 稳定；使用 workload token 提交
   `project.annotate` command，断言 `SUCCEEDED`、单条 Inbox 和 `SENT` Result Outbox。
2. 从真实 Fluxion Mongo summary record 读取 record/source version，启动 `ProjectDiagnosticWorkflow`
   到本地 Temporal namespace/task queue，等待 result 并断言 `ActivityTaskCompleted`、Mongo
   `binding_snapshots` tenant/workspace/version 和 snapshot ID。
3. 通过 Bids API 登录、创建项目并提交真实审批。Bids API 先持久化 `APPROVAL_SUBMITTED` 和 outbox；
   本地 gate 再调用同一真实 Conductor human task completion endpoint，避免等待可变的 outbox 调度延迟，
   随后由真实 Bids worker 将项目收敛为 `READY`。
4. 通过 Bids API 创建 `RENOVATION_CONSTRUCTION` tender，等待 Bids summary 投影；启动真实
   `bids_record_hub_tender_diagnostic` Conductor workflow，断言 snapshot ID/hash、safe summary、Mongo
   snapshot 数量和 tender ID。
5. Temporal history 和 Conductor result 都检查不可出现 `P3-WORKFLOW-SECRET-MARKER`；只允许 safe
   summary、schema/version、record ref 和 snapshot metadata。

Bids 的业务 envelope 使用示例组织租户 `00000000-0000-0000-0000-000000000001`，binding machine policy
因此按该真实租户配置；workspace 仍固定为 `workspace-p3-bids`。Fluxion 使用独立的
`tenant-p3-fluxion/workspace-p3-fluxion` scope。fixture manifest 中的 binding policy 只允许
`recordhub.binding.snapshot` diagnostic 读取，不授予 owner 写入权限。

## 最近一次 live PASS

2026-09-19 Darwin arm64 原生运行通过，evidence manifest 中记录四仓库 commit 和隔离端口。通过条件包括：

- Fluxion command `SUCCEEDED`，Inbox=1，Result Outbox `SENT`=1；
- Temporal workflow 完成，ActivityTaskCompleted 存在，raw command marker 不存在；
- Fluxion 与 Bids snapshot 均写入 Mongo `binding_snapshots`，Bids Conductor result 含 snapshot hash；
- Bids 项目审批、tender summary、Conductor diagnostic workflow 完成；
- 最终 `workflow-e2e.json` 与 `db-assertions/p3-402-workflow.json` 标记 `status=PASS`。

P3-308 的 transaction rollback/ACK-loss/NATS outage 完整矩阵、P3-403..409 的恢复、轮换、容量、升级和
最终 Phase 3 报告仍按任务表保留，不能用本 gate 替代。
