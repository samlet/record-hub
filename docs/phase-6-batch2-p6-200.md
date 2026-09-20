# Phase 6 Batch 2：P6-200 connector registry

P6-200 固化 connector registry 的精确 key `(connector,event,schemaVersion)`、owner/contract
字段、SDK 兼容窗口和 CAS lifecycle。未知 key、禁用 connector、兼容窗口外 SDK 或 stale revision
都 fail closed；disable 只停止新请求并 drain 在途工作。

机器可读 contract 位于 [`p6-200-connector-registry-spec.json`](../deploy/local/p6/p6-200-connector-registry-spec.json)，
验收结果位于 [`phase-6-connector-registry.json`](phase-6-connector-registry.json)。入口是 `make p6-200`。

这是静态 contract gate：`liveTraffic=false`，不改变 connector 状态、不发送 workflow/NATS 工作，也不解除
P6-000 的 Phase 5 前置条件。
