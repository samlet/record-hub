# P5-400 Security/PII/sealed-data supply-chain scan

P5-400 固化四仓库的安全数据供应链扫描边界：Git 工作树中的 private key、云访问 key、个人/聊天 token 和 literal bearer token；Record Hub safe projection；owner-side allowlist/redaction；gitleaks 配置、Go module integrity 和 evidence manifest contract。静态扫描明确排除 `.git`、构建产物、依赖缓存、`c123.db`、模块 checksum/lock 文件以及扫描器自身的 pattern 文本，避免把工具规则和本地数据库误报成业务泄漏。

静态入口：`make p5-400`。它检查四仓库当前树的 secret marker、Settlement/association 安全投影字段、Approver/Fluxion/Bids 的 allowlist/redaction boundary、`.gitleaks.toml`/secret scan/evidence contract 和四仓库 commit boundary，并生成 `build/evidence/phase5/p5-400-*/p5-400.json`。

当前状态为 `PARTIAL`：静态扫描应全部 PASS；Temporal/Conductor history、运行日志、metrics、DLQ、live evidence 和 immutable artifact 的导出扫描需要隔离四 owner runtime 与正式 evidence runner，当前 live 为 `SKIPPED`。静态 source scan 不能替代运行时数据导出扫描，也不能把当前工作树结果外推成 Production GA 结论。

解除条件：提供 topology file、脱敏后的 history/log/metrics/DLQ/evidence export、artifact manifest 和安审/运维签字，设置 `RECORD_HUB_P5_SECURITY_LIVE=1` 后执行完整扫描。发现任意 token、PII、sealed bid、金额、银行/发票附件或原始 workflow snapshot 时，立即停止发布、旋转凭据并保留旧 worker/fallback/rollback artifact。
