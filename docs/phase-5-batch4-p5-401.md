# P5-401 Single-tenant canary 与完整观察窗口

P5-401 固化 Production GA 前的最小灰度：只允许一个低敏 tenant/workspace 的新流量，保留旧 worker、旧 schema reader、Fluxion local fallback 和 rollback artifact。观察窗口必须覆盖完整 retry/backoff、retention 和 reconciliation 周期，并按分钟采集 terminal outcome、materialization lag、delivery retry/dead、projection backlog/DLQ、finding/recovery、跨 scope 拒绝、safe-payload scan、SLO 和 error budget。

静态入口：`make p5-401`。它检查 P4-501 live 前置、单 tenant/workspace scope、观察信号、停止/回滚条件、evidence/签字 contract 和四仓库 commit boundary，并生成 `build/evidence/phase5/p5-401-*/p5-401.json`。

当前状态为 `PARTIAL`：静态检查应全部 PASS；P4-501 尚未完成，真实灰度、完整 retry/retention/reconciliation 观察窗口、stop/rollback drill 和 owner signoff 当前为 `SKIPPED`。本地普通服务或一次性 smoke 不能替代观察窗口。

解除条件：先清除 P4-501 的全部 live gate，再提供 tenant/workspace allowlist、四 owner runtime、minute observation exporter、rollback harness 和签字 evidence，设置 `RECORD_HUB_P5_CANARY_LIVE=1` 后执行。窗口内任何跨 scope、重复终态副作用、sealed-data 泄漏、SLO breach、dead 增长或对账不一致都必须停止 publisher 并回滚。
