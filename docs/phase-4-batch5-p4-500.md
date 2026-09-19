# P4-500 Release Candidate Manifest

P4-500 固定四个 workflow owner 的 commit、源码/contract/migration/config digest，以及六个 immutable build artifacts。入口是 `make p4-release-candidate`，输出为 [phase-4-release-candidate-manifest.json](phase-4-release-candidate-manifest.json)。

当前 candidate 为 `rc-cbaa0a357110`：Record Hub、Approver API/Worker、Fluxion Server、Bids API/Worker 均已构建，artifact SHA-256、字节数、构建命令和相对路径已写入 manifest；baseline、contract inventory、evidence contract 均 PASS。`c123.db` 仍只作为 Record Hub untracked 文件列出，不进入 digest 或 artifact。

P4-500 本身已完成静态冻结。P4-501 仍不得发布：完整 rotation/restore/mixed-load/rolling-rollback 必须在隔离四 owner topology 中执行，不能用本机普通服务或 `SKIPPED` 代替。
