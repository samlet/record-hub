# 本地 MongoDB

本地开发使用 MongoDB 8.0.32 单节点 replica set `record-hub-rs`。Compose 首次启动时生成内部 keyfile、初始化 replica set，并幂等创建只拥有 `record_hub` 数据库 `readWrite` 权限的应用用户。凭据只从环境变量读取，不写入仓库或 Compose 文件。

Compose 显式启用 glibc rseq，以兼容使用 Linux 6.19—7.0.13 内核的 Docker Desktop VM；该设置只属于本地开发拓扑，生产部署仍须采用 MongoDB 官方支持的操作系统与内核组合。

要求：

- 使用 replica set，以覆盖 transaction 和 change stream 的真实运行条件；
- Record Hub 使用独立数据库用户；
- 其他业务系统不持有 MongoDB 凭据；
- 测试单文档 CAS、多文档事务回滚、唯一索引冲突和 change stream 恢复；
- `data/`、备份文件和 keyfile 不提交仓库。

## 启动与验收

### Homebrew native 安装

macOS 本地可以使用 MongoDB 官方 Homebrew tap，不需要 Docker：

```bash
brew tap mongodb/brew
brew install mongodb-community@8.0
brew services start mongodb/brew/mongodb-community@8.0
```

公式生成的默认配置使用单节点 replica set `rs0`。如果该实例已有其他本地数据，保持
`rs0` 不变，不执行清库或重命名；Record Hub native smoke 使用独立的 `record_hub` 数据库：

```bash
export RECORD_HUB_MONGODB_URI='mongodb://127.0.0.1:27017/record_hub?replicaSet=rs0&directConnection=true'
make mongo-smoke
```

该模式默认没有 MongoDB 用户认证，只适合受控开发机。需要验证 Record Hub 文档中带
`record-hub-rs`、keyfile 和独立应用用户的拓扑时，使用下方 Docker 配置或单独的 native
`mongod.conf`/数据目录，不要修改共享的 Homebrew 实例。

先在当前 shell 设置四个本地凭据；建议用密码管理器或 `openssl rand -hex 24` 生成，不要写入被跟踪文件：

```bash
export MONGODB_ROOT_USERNAME=record_hub_root
export MONGODB_ROOT_PASSWORD='<local-random-password>'
export RECORD_HUB_MONGODB_USERNAME=record_hub
export RECORD_HUB_MONGODB_PASSWORD='<different-local-random-password>'
```

启动并等待初始化完成：

```bash
make mongo-up
```

设置应用连接 URI 后运行真实驱动 smoke：

```bash
export RECORD_HUB_MONGODB_URI='mongodb://record_hub:<url-encoded-password>@127.0.0.1:27017/record_hub?replicaSet=record-hub-rs&authSource=record_hub&directConnection=true'
make mongo-smoke
```

Smoke 会验证多文档事务提交/回滚、复合唯一索引、基于版本的 CAS 和 change stream。停止服务但保留 named volume：

本地 URI 使用 `directConnection=true`，因为单节点在 Compose 网络内公布的成员名是 `mongodb`，宿主机不解析该内部 DNS 名。生产 replica set 不应使用这个选项。

```bash
make mongo-down
```

`docker compose down --volumes` 会永久删除本地 MongoDB 数据和自动生成的 keyfile，不包含在普通停止命令中。
