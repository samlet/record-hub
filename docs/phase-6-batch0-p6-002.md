# Phase 6 Batch 0：P6-002 evidence contract

P6-002 固定后续 connector、schema、workflow 和 event gate 必须提交的 evidence 字段：source commit、
contract/fixture digest、redaction、scope、failure matrix、rollback、operator audit 和 topology。
机器可读结果位于 [`phase-6-evidence-contract.json`](phase-6-evidence-contract.json)。

入口是 `make p6-002`。本门只验证 evidence contract，不启动真实 connector、不清除 Phase 5 blocker，
也不把静态 `PASS` 当作 live 或 GA `PASS`。
