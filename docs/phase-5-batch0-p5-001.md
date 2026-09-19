# Phase 5 Batch 0：P5-001 RC baseline

P5-001 已完成，固定四仓库在 Phase 5 开始前的 release-candidate baseline：每个仓库的
commit、tracked source digest、contract/migration/config digest、工作树状态和未跟踪文件清单，
以及 Record Hub、Approver API/worker、Fluxion server、Bids API/worker 六个 immutable artifact 的
SHA-256 与字节数。

入口为 `make p5-001`，脚本是 `scripts/bootstrap-p5-baseline.sh`，结果写入
[`phase-5-baseline-manifest.json`](phase-5-baseline-manifest.json)，构建产物写入被 gitignore 的
`build/p5-baseline-artifacts/`。`c123.db` 等未跟踪文件会被列出但不进入 digest。只有在四仓库
tracked worktree clean 且六个 artifact 全部成功生成时，manifest 才会标记 `PASS`。

本门只冻结 baseline，不把 P4-501、P5-G1～G7 或生产 GA 标记为 PASS；这些仍由各自 live gate 独立验收。
