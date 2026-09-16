# 原生隔离验收拓扑

`make p2-isolated-core` 会在当前用户的临时目录建立一套不会复用共享服务的最小拓扑：

| 组件 | 默认地址/端口 | 生命周期 |
| --- | --- | --- |
| MongoDB replica set | `127.0.0.1:37018` | 临时 dbpath，脚本退出成功后删除 |
| NATS JetStream | `127.0.0.1:14223`，监控 `:18223` | 临时 store，脚本退出成功后删除 |
| human Dex | `http://127.0.0.1:15566/dex`，telemetry `:15568` | 临时 SQLite、随机用户/client secret |
| workload issuer | `http://127.0.0.1:15557/workload` | 临时 RSA key、随机 client secret |
| Record Hub API | `127.0.0.1:18081` | 使用上述临时 Mongo/NATS/issuer |

脚本首先检查所有端口；任一端口被占用会直接失败并要求通过对应的
`RECORD_HUB_P2_*_PORT` 覆盖，不会停止或修改现有服务。Mongo、NATS、Dex、workload issuer
和 Record Hub 均由脚本自身启动，退出时只向自身 PID 发送信号。失败时保留临时目录并输出
路径，便于查看有界日志；成功时删除临时目录。

Temporal/Conductor 引擎基线另由 `make p2-engine-foundation` 验收。该命令使用专用的
Temporal gRPC `:17233`、Temporal UI `:18233` 和 Conductor HTTP `:18080`，并把 Temporal
SQLite 与 Conductor 日志放在独立临时目录；不会停止或修改共享的 `:7233`/`:8080`。四个业务
进程的接入尚未纳入该命令，记录在 P2-0-006b，完成前不得宣称整套业务拓扑已通过。

## 覆盖端口

例如：

```bash
RECORD_HUB_P2_MONGO_PORT=37118 \
RECORD_HUB_P2_NATS_PORT=14323 \
RECORD_HUB_P2_NATS_MONITOR_PORT=18323 \
RECORD_HUB_P2_DEX_PORT=15666 \
RECORD_HUB_P2_DEX_TELEMETRY_PORT=15668 \
RECORD_HUB_P2_WORKLOAD_PORT=15657 \
RECORD_HUB_P2_API_PORT=18181 \
make p2-isolated-core
```

## 已验证内容

- MongoDB transaction、唯一索引、CAS、change stream；
- NATS JetStream stream/consumer、ACK、deduplication、DLQ 和 256 KiB bound；
- Dex 四个 Web client 的 discovery/JWKS、Authorization Code + PKCE、redirect/PKCE 负向；
- workload issuer 两个独立 client 的 discovery/JWKS、RS256、`iss/sub/aud/exp/iat/scope`、
  错误 secret/scope 负向；
- Record Hub `/healthz`、`/readyz`、metrics、Web login 入口；
- 使用有效 workload token 的 Binding 请求命中 exact machine policy（缺失记录返回
  `RECORD_NOT_FOUND`），跨 workspace 返回 `FORBIDDEN`。

该拓扑是本地/CI 验收工具，不是生产部署模板。workload issuer 的临时签名 key 不持久化，
生产必须替换为独立 Authorization Server 或平台 workload identity。
