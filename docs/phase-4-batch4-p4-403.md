# P4-403 Secret/PII/sealed-data evidence scan

验收范围包括四仓库 Git 工作树、配置、源码、构建 evidence、日志/DLQ 导出和 metrics 文本。扫描拒绝 private key、云访问 key、service token 明文；Bids sealed 字段只能出现在 allowlist contract validator/negative fixture，不能进入日志、metrics、DLQ 或安全投影。Evidence 只能保存 hash、版本、scope、状态和 bounded safe reason。

`scripts/verify-p4-batch4.sh` 的 `P4-403-evidence-scan` 排除构建目录和本地未跟踪 Mongo 文件 `c123.db`，并要求四 owner 存在 redaction/allowlist 证据。发现任一真实 secret 或 sealed payload 时立即阻止 release gate；当前扫描结果为静态检查，live 日志/DLQ/metrics 导出待隔离拓扑。
