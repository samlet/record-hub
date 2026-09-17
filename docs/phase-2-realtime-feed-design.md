# P2-2 Realtime Read Feed 设计

状态：MVP vertical slice 已完成（2026-09-17）

## 边界

Record Hub 提供 `GET /api/v1/tables/{tableId}/records/stream` 的 SSE 只读流。连接必须带
`tenantId`、`workspaceId`，并由当前 human principal 通过 `record.read` membership policy 授权。
事件只包含记录引用、版本、变更类型、scope、来源事件 ID 和时间戳；客户端收到事件后重新读取记录，
不把记录正文放入 SSE、日志或 workflow history。

## 可靠性与配额

- 事件 ID 是进程内单调递增的 SSE `id`，默认保留最近 2,048 条事件；它不是跨重启的 durable offset。
- `Last-Event-ID` 与 `cursor` 均支持断线恢复。游标仍在保留窗口内时先回放匹配 scope 的事件，再进入 live；
  过旧返回 `409 CURSOR_RESET_REQUIRED`，客户端应重新查询记录并从无 cursor 建立新连接。
- 每个连接有 64 条事件的默认缓冲、64 KiB 单事件上限；默认每个 principal/scope 最多 16 条连接，进程总计
  256 条。消费者不读取导致缓冲满时，连接被关闭并计数；Mongo 写入不会因实时订阅不可用而失败。
- 连接默认每 5 秒重新执行 workspace membership 授权、每 15 秒发送 heartbeat。撤权后在一个授权检查周期内
  终止连接；在检查周期内不会继续接受新的事件。
- Mongo 普通记录写入和 live projection apply 都发布引用事件；projection retry 通过 source event dedup key
  避免重复通知。rebuild staging 不发布事件，只有 read pointer 切换后的 live continuation 才发布。

## 游标与重建语义

SSE offset 只代表 Record Hub 当前进程的有界内存窗口。Mongo projection archive、checkpoint 和 read pointer
仍然是 durable recovery/rebuild 机制，不能以 SSE cursor 代替。重建期间 staging 事件不会泄露到 feed；切换
完成后，新的 live event 会继续使用同一条 feed，但客户端必须按 `recordVersion` 重新读取并处理可能的旧版本。

## 已知限制与下一批

当前实现是单进程 fan-out，不宣称跨实例广播或无限历史；速率令牌桶也尚未纳入本批。Beta 部署需要在
网关/共享 broker 层增加连接亲和或 durable feed（例如按 scope 的 NATS subject），并补充每 principal/workspace
速率指标、SDK helper、浏览器自动刷新和真实撤权 live E2E。该工作进入 P2-2 的后续验收，不改变当前 API 的
reset 语义。
