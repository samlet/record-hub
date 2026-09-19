# P4-501 完整 Live 发布验收

验收入口是 `make p4-batch5`，规格为 [p4-batch5-spec.json](../deploy/local/p4/p4-batch5-spec.json)。P4-G1～G5 分别覆盖 credential/JWKS rotation、Mongo/PostgreSQL/NATS restore、mixed load/drain、rolling upgrade/rollback 和跨 owner fault matrix。

当前预检结果：release candidate manifest `PASS`，五个 case 的静态配置检查 `PASS`，live `5/5 SKIPPED`。缺少隔离四 owner topology、operator credentials、old/new artifacts、evidence root 和专用 fault/load harness；本机已安装的普通服务不能证明 P4-G1～G5 的跨租户、恢复和滚动回滚结论。

因此 P4-501 明确保持 `SKIPPED`，不得进入 Beta 流量。只有设置完整环境并使用 `RECORD_HUB_P4_BATCH5_LIVE=1` 运行受批准 harness、且五个 case 均为 `PASS`，才能将该任务改为 `DONE`。
