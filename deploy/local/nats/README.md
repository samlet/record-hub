# 本地 NATS JetStream

本地开发使用 NATS Server 2.14.6 和 JetStream file storage。`nats-init` 幂等创建 `DOMAIN_EVENTS`、`DEAD_LETTERS` 以及 Approver、Fluxion、Bids 三个 durable pull consumer。

要求：

- 启用 JetStream file storage；
- 每个系统使用独立 NATS principal；
- producer 只能发布自己的 `events.<system>.>`；
- command 权限按 target system 收敛；
- 使用 durable pull consumer、explicit ACK、MaxDeliver 和 DLQ；
- 不提交 credential、seed 或私钥。

详细设计见 [事件契约](../../../docs/event-contract.md)。

## 启动与验收

```bash
make nats-up
export RECORD_HUB_NATS_URL='nats://127.0.0.1:4222'
make nats-init
make nats-smoke
```

`nats-smoke` 会再次执行幂等初始化，并验证 file storage 拓扑、256 KiB 消息上限、消息 ID 去重、按 source filter 的 pull delivery、显式 ACK 和 DLQ 路由。

停止服务但保留 JetStream named volume：

```bash
make nats-down
```

身份与 subject 权限在 `RH-M1-012` 加入；在此之前 NATS 只允许绑定到本机回环地址，不得作为共享环境配置使用。
