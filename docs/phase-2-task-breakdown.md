# Integration Beta 任务分解

- 状态：In progress
- 方案：[phase-2-evaluation.md](phase-2-evaluation.md)
- 需求：[phase-2-requirements.md](phase-2-requirements.md)
- 开始日期：2026-09-17

状态沿用 MVP 定义：`TODO`、`IN_PROGRESS`、`PARTIAL`、`DONE`、`BLOCKED`、`SKIPPED`、
`DEFERRED`。Live 依赖不可用时必须记录 `SKIPPED` 与重试条件，不能以 fake 替代。

## P2-0：身份与隔离验收基线

| ID | 任务 | 状态 | 验收 |
| --- | --- | --- | --- |
| P2-0-001 | Human/workload identity ADR | DONE | ADR-0003 冻结 issuer、claim、scope、policy、本地/Beta/生产边界 |
| P2-0-002 | Exact machine policy runtime config | DONE | 有界严格 JSON；issuer/subject/audience/scope/tenant/workspace/purpose/resource exact match；负向测试通过 |
| P2-0-003 | Ephemeral local workload issuer | DONE | client credentials、临时 RS256、5 分钟 token、discovery/JWKS；仅本地/CI |
| P2-0-004 | Workload token live contract gate | DONE | Fluxion/Bids 独立 client；错误 secret/scope 拒绝；`make p2-workload-identity-smoke` 通过 |
| P2-0-005 | 隔离 Mongo/NATS/Dex/Workload issuer/API 拓扑 | TODO | 专用端口/目录/数据库；一条命令启动、探测、验收、清理，不影响现有服务 |
| P2-0-006 | 隔离 Temporal/Conductor/四应用拓扑 | TODO | 真实 engine、三个 producer、Record Hub 受监督运行，保存版本与有界日志 |
| P2-0-007 | 补跑 Dex 浏览器角色矩阵 | TODO | login/callback/logout、OWNER/EDITOR/VIEWER、撤权与跨租户负向无跳过 |
| P2-0-008 | 补跑双引擎 Binding live/restart | TODO | workload token、真实 Temporal/Conductor、snapshot/replay/restart 无跳过 |
| P2-0-009 | 补跑四系统 outage/restart/rotation | TODO | producer backlog/recovery、全进程 restart、JWKS/client secret rotation 无跳过 |
| P2-0-010 | P2-0 基线验收报告 | TODO | 命令、版本、commit、PASS/SKIPPED 与遗留风险完整 |

## 后续批次

| 批次 | 状态 | 范围 |
| --- | --- | --- |
| P2-1 Operable Data | TODO | Schema diff/migration、source/mapping registry、projection rebuild、SLO |
| P2-2 Realtime Read Feed | TODO | SSE、cursor、背压、撤权和配额 |
| P2-3 Controlled Command | TODO | policy、receipt、owner Inbox、单一低风险 command |
| P2-4 Approval/Beta | TODO | Approver connector/试点、备份恢复、升级回滚、Beta 准入 |

