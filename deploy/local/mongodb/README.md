# 本地 MongoDB

本目录后续保存不含凭据的 MongoDB 本地开发说明和初始化脚本。

要求：

- 使用 replica set，以覆盖 transaction 和 change stream 的真实运行条件；
- Record Hub 使用独立数据库用户；
- 其他业务系统不持有 MongoDB 凭据；
- 测试单文档 CAS、多文档事务回滚、唯一索引冲突和 change stream 恢复；
- `data/`、备份文件和 keyfile 不提交仓库。

