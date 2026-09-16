# Record Hub 总体架构

- 状态：Proposed
- 目标系统：Approver、Fluxion、Bids
- 服务端：Go
- Web 控制台：TypeScript
- 主要基础设施：MongoDB、NATS JetStream、Dex

## 1. 目标

Record Hub 提供以下共享能力：

1. 类似多维表格的动态字段、标签、关联、过滤和视图。
2. 版本化 Schema Registry，以及到 Schema.org 的可选语义映射。
3. 三个业务系统的安全、最终一致状态投影和统一速查。
4. Workflow 节点对记录快照、查询和受控命令的稳定引用。
5. 基于 NATS JetStream 的领域事件分发、重放和消费者隔离。
6. 可审计的外部命令入口，把写操作路由回数据所属系统。

## 2. 非目标

Record Hub 不负责：

- 取代业务系统数据库或 Workflow Engine；
- 直接决定招标、履约、付款或审批状态；
- 提供跨数据库、消息系统和 Workflow Engine 的全局 ACID 事务；
- 把任意表格编辑自动提升为领域事件；
- 保存完整投标文件、合同文件或其他大对象；
- 通过 tag 代替认证、租户隔离或字段授权。

## 3. 系统拓扑

```text
Approver/PostgreSQL -- Outbox --\
Fluxion/PostgreSQL  -- Outbox ----> NATS JetStream ---> Record Hub Projectors
Bids/PostgreSQL     -- Outbox --/           |                  |
                                              |                  v
                                              |             MongoDB
                                              |        schemas/records/views
                                              |                  |
                                              v                  v
                                     Domain consumers       Record Hub API/UI

Temporal/Conductor Workflow
        |
        +-- Workflow Binding API --> snapshot/ref/receipt
        |
        +-- Command Gateway -------> owning system API/Inbox
```

所有业务系统都先在自己的数据库事务中写业务事实和 Outbox，再异步发布到 JetStream。JetStream 发布成功不能替代本地 Outbox 状态机。

## 4. 逻辑模块

首期模块位于同一个后端进程：

| 模块 | 职责 |
| --- | --- |
| Schema Registry | Schema 草稿、发布、兼容性、语义映射 |
| Record Service | 动态记录、版本、CAS、标签和关联 |
| View Service | 过滤、排序、列配置和物化视图 |
| Index Policy | 仅为已发布 schema 顶层字段创建 bounded Mongo 索引 |
| Projection Consumer | 消费领域事件并更新只读投影 |
| Workflow Binding | 解析引用、创建快照、返回 hash/版本 |
| Command Gateway | 把受控写操作发送给 owner system |
| Delivery/Inbox | 消费去重、重试、死信和处理凭据 |
| Audit | Schema、记录、权限、命令和重放审计 |

Go 服务端按模块化单体组织，通过接口隔离存储、消息和身份适配器。禁止按表或按基础设施提前拆微服务。

## 5. 数据所有权

| 数据 | 事实来源 | Record Hub 中的形态 |
| --- | --- | --- |
| Approver Application/Process/Task | Approver | 只读安全投影和引用 |
| Fluxion Project/Stage/Decision | Fluxion | 只读安全投影和引用 |
| Bids Tender/Contract/Fulfillment | Bids | 只读安全投影和引用 |
| 自定义协作记录、tag、view | Record Hub | 可编辑事实 |
| 付款、定标、签署、审批决定 | 所属业务系统/Approver | 摘要、状态和证据引用 |

外部投影使用 `(sourceSystem, sourceType, sourceId)` 唯一键。只有事件的 `aggregateVersion` 大于当前 `sourceVersion` 时才能前进；版本间隙进入待恢复状态，不静默跳过。

## 6. 一致性模型

- 传输：至少一次。
- 消费：Inbox 去重，处理结果幂等。
- 投影：最终一致，暴露 `lastEventId`、`sourceVersion`、`syncedAt` 和 lag。
- 记录写入：单记录原子；多记录操作按需使用 MongoDB transaction。
- 外部业务写入：Command + expected version + result receipt。
- 跨系统失败：重试或 Saga 补偿，不伪装成全局事务。

## 7. 可用性边界

- Record Hub 不可用时，三个业务系统继续写自己的事实和 Outbox。
- JetStream 不可用时，Outbox 保留待发布事件。
- 投影消费者恢复后按 durable cursor 继续处理。
- Record Hub 只读投影不可用于绕过 owner system 的业务校验。
- Workflow 若依赖 Record Hub 输入，必须声明超时、重试和不可用分支。

## 8. 安全原则

- 人和机器身份分离。
- 每个调用方向使用独立 client/service identity。
- 所有数据至少按 tenant、workspace、table、record、field 五层授权。
- Bids 未开标投标内容、报价和其他供应商数据默认禁止投影。
- Workflow history 和事件 payload 只放小型、安全字段及引用。
- 所有 Schema 发布、字段权限修改、命令执行和死信重放均写审计。
