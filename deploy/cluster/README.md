# 本地集群拓扑

该拓扑用于本机容量和故障演练，不是生产部署模板。

## 启动

```powershell
docker compose -p whu-snack-go-cluster `
  --env-file deploy/cluster/env.example `
  -f docker-compose.cluster.yml up -d --build
```

入口为 `http://127.0.0.1:8088`。

## 验证

```powershell
docker exec whu-snack-go-cluster-rabbitmq-1-1 rabbitmqctl cluster_status
docker exec whu-snack-go-cluster-rabbitmq-1-1 rabbitmqctl list_queues name type state online
docker exec whu-snack-go-cluster-redis-sentinel-1-1 redis-cli -p 26379 SENTINEL master ticketing-master
docker exec whu-snack-go-cluster-mysql-replica-1 mysql -uroot -pcluster-root-password -e "SHOW REPLICA STATUS\G"
powershell.exe -ExecutionPolicy Bypass -File tests/load/run_cluster_staircase.ps1
```

## 停止

保留数据卷：

```powershell
docker compose -p whu-snack-go-cluster `
  --env-file deploy/cluster/env.example `
  -f docker-compose.cluster.yml down
```

删除该集群的测试数据卷：

```powershell
docker compose -p whu-snack-go-cluster `
  --env-file deploy/cluster/env.example `
  -f docker-compose.cluster.yml down -v
```

MySQL 从库仅用于复制验证；应用仍只连接主库，未实现自动写库故障转移。
