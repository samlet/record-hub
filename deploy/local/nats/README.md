# 本地 NATS JetStream

本地开发使用 NATS Server 2.14.6 和 JetStream file storage。`nats-init` 幂等创建 `DOMAIN_EVENTS`、
`APPROVAL_COMMANDS`、`OWNER_COMMANDS`、`COMMAND_RESULTS`、`DEAD_LETTERS` 以及三个 owner command Inbox、三个 projection 和一个 command-result durable pull consumer。

要求：

- 启用 JetStream file storage；
- 每个系统使用独立 NATS principal；
- producer 只能发布自己的 `events.<system>.>` 和 `results.<system>.>`；
- command 权限按 target system 收敛；
- 使用 durable pull consumer、explicit ACK、MaxDeliver 和 DLQ；
- 不提交 credential、seed 或私钥。

详细设计见 [事件契约](../../../docs/event-contract.md)。

## 启动与验收

先为五个独立 identity 设置随机本地密码：

```bash
export NATS_ADMIN_PASSWORD='<local-random-password>'
export NATS_APPROVER_PASSWORD='<different-local-random-password>'
export NATS_FLUXION_PASSWORD='<different-local-random-password>'
export NATS_BIDS_PASSWORD='<different-local-random-password>'
export NATS_RECORD_HUB_PASSWORD='<different-local-random-password>'
make nats-up
export RECORD_HUB_NATS_URL='nats://record-hub-admin:<url-encoded-password>@127.0.0.1:4222'
make nats-init
make nats-smoke
```

`nats-smoke` 会再次执行幂等初始化，并验证 file storage 拓扑、256 KiB 消息上限、消息 ID 去重、按 source filter 的 pull delivery、显式 ACK 和 DLQ 路由。

停止服务但保留 JetStream named volume：

```bash
make nats-down
```

## 权限负向验收

三个 producer 只能发布各自的 `events.<system>.>`/`results.<system>.>`，并只能使用自己预建的 command Inbox consumer。Projector 只能使用三个预建 projection consumer 与一个 command-result consumer 的精确 JetStream API、ACK subject 和 `dlq.record-hub.>`；不能直接订阅领域/命令 subject、发布领域事件或修改 topology。

```bash
export NATS_APPROVER_URL='nats://approver-relay:<url-encoded-password>@127.0.0.1:4222'
export NATS_FLUXION_URL='nats://fluxion-relay:<url-encoded-password>@127.0.0.1:4222'
export NATS_BIDS_URL='nats://bids-relay:<url-encoded-password>@127.0.0.1:4222'
export NATS_RECORD_HUB_URL='nats://record-hub-projector:<url-encoded-password>@127.0.0.1:4222'
make nats-permissions-smoke
```
