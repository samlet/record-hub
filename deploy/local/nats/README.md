# 本地 NATS JetStream

本目录后续保存本地 NATS JetStream 配置、初始化脚本和 subject 权限样例。

要求：

- 启用 JetStream file storage；
- 每个系统使用独立 NATS principal；
- producer 只能发布自己的 `events.<system>.>`；
- command 权限按 target system 收敛；
- 使用 durable pull consumer、explicit ACK、MaxDeliver 和 DLQ；
- 不提交 credential、seed 或私钥。

详细设计见 [事件契约](../../../docs/event-contract.md)。

