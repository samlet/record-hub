# P4-502 单 Tenant Beta 灰度与观察窗口

P4-502 的最小灰度范围是一个明确 tenant/workspace，必须在 P4-501 的 P4-G1～G5 全部 PASS 后启动。灰度期间只允许该 tenant 的 approval request，保留旧 worker/旧 artifact 和 OFF 可回退 flag。

观察窗口至少覆盖一个完整 retry/backoff、retention 和 reconciliation 周期。每 1 分钟记录 terminal outcome、materialization lag、delivery retry/dead、projection backlog/DLQ、finding/recovery、跨 scope 拒绝和 safe-payload scan；所有 label 保持有限基数。窗口内出现跨租户、重复 terminal side effect、sealed-data 泄漏、SLO breach、未知/dead 增长或对账不一致，立即停止 publisher，按 Batch 4 runbook 回滚。

当前状态：`SKIPPED`。P4-501 live gate 尚未具备，未启动任何灰度流量，也没有把本机服务运行当作观察窗口证据。
