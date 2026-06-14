# WHU Snack Go

武大零食超市系统 - 基于 Go + Vue 3 的校园零食外卖平台

## 项目简介

WHU Snack Go 是一个专为武汉大学校园设计的零食外卖平台，采用微服务架构设计，支持商品浏览、购物车、订单管理、秒杀活动等核心功能。系统采用前后端分离架构，后端使用 Go 语言开发，前端使用 Vue 3 + Vite 构建。

## 技术栈

### 后端
- **Go 1.25** - 核心语言
- **Gin** - HTTP 框架
- **GORM** - ORM 框架
- **Redis** - 缓存层
- **RabbitMQ** - 消息队列
- **MySQL** - 持久化存储
- **Snowflake** - 分布式 ID 生成

### 前端
- **Vue 3** - 前端框架
- **Vite** - 构建工具
- **Element Plus** - UI 组件库
- **Pinia** - 状态管理
- **Vue Router** - 路由管理

### 基础设施
- **Docker Compose** - 容器编排
- **Prometheus** - 监控指标
- **Grafana** - 可视化面板
- **Nginx** - 反向代理

## 核心功能

### 用户模块
- 用户注册与登录 (JWT 双 token 认证)
- 个人信息管理
- 修改密码
- 收货地址管理

### 商品模块
- 商品列表展示 (支持分类、搜索、排序)
- 商品详情查看
- 分类管理
- 库存管理 (Redis 缓存 + 预热)

### 订单模块
- 购物车功能
- 普通下单 (RabbitMQ 异步化)
- 订单状态流转
- 取消订单 / 申请退款
- 订单超时自动取消

### 秒杀模块
- 秒杀活动管理
- 秒杀令牌机制
- Lua 脚本原子扣减库存
- 限购与防超卖
- 预热与缓存

### 管理后台
- 仪表盘统计
- 商品管理 (上下架)
- 订单管理
- 用户管理
- 秒杀活动管理

## 项目结构

```
whu_snack_go/
├── backend/
│   ├── common/          # 公共组件 (JWT, Redis, RabbitMQ, 限流)
│   ├── config/          # 配置管理
│   ├── container/       # 依赖注入容器
│   ├── controller/      # 控制器层
│   ├── models/          # 数据模型
│   ├── pkg/             # 公共包 (错误处理, 日志, 响应)
│   ├── repository/       # 数据访问层
│   ├── service/         # 业务逻辑层
│   ├── metrics/         # Prometheus 监控
│   └── main.go          # 入口文件
├── frontend/
│   ├── src/
│   │   ├── api/         # API 请求
│   │   ├── components/  # 公共组件
│   │   ├── layouts/     # 布局
│   │   ├── router/      # 路由配置
│   │   ├── stores/      # Pinia 状态管理
│   │   ├── views/       # 页面视图
│   │   └── main.js      # 入口文件
├── deploy/              # 部署配置
├── monitoring/          # 监控配置
├── tests/               # 测试脚本
│   └── load/           # 压测脚本
└── docker-compose.yml  # 容器编排
```

## 快速开始

### 前置要求

- Go 1.25+
- Node.js 18+
- Docker & Docker Compose
- MySQL 8.0+
- Redis 7.0+
- RabbitMQ 3.12+

### 后端配置

1. 复制配置文件：
```bash
cp backend/config/config.example.yaml backend/config/config.yaml
```

2. 修改配置文件，配置数据库、Redis、RabbitMQ 连接信息。

3. 启动后端服务：
```bash
cd backend
go run main.go -config config/config.yaml
```

### 前端配置

1. 安装依赖：
```bash
cd frontend
npm install
```

2. 启动开发服务器：
```bash
npm run dev
```

### Docker Compose 启动 (推荐)

```bash
# 启动完整服务 (后端 + 前端 + MySQL + Redis + RabbitMQ)
docker-compose up -d

# 启动监控系统
docker-compose -f monitoring/docker-compose.monitoring.yml up -d
```

## API 接口

### 用户接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/register | 用户注册 |
| POST | /api/v1/login | 用户登录 |
| POST | /api/v1/refresh | 刷新 Token |
| GET | /api/v1/user/info | 获取用户信息 |
| PUT | /api/v1/user/info | 更新用户信息 |
| PUT | /api/v1/user/password | 修改密码 |

### 商品接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/products | 商品列表 |
| GET | /api/v1/products/:id | 商品详情 |
| GET | /api/v1/categories | 分类列表 |

### 订单接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/orders | 创建订单 |
| GET | /api/v1/orders | 订单列表 |
| GET | /api/v1/orders/:id | 订单详情 |
| POST | /api/v1/orders/:id/cancel | 取消订单 |
| POST | /api/v1/orders/:id/refund | 申请退款 |

### 秒杀接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/seckill/activities | 秒杀列表 |
| GET | /api/v1/seckill/activities/:id | 秒杀详情 |
| POST | /api/v1/seckill/activities/:id/token | 获取秒杀令牌 |
| POST | /api/v1/seckill/activities/:id/execute | 执行秒杀 |

### 管理接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/admin/dashboard | 仪表盘 |
| POST | /api/v1/admin/products | 创建商品 |
| PUT | /api/v1/admin/products/:id | 更新商品 |
| GET | /api/v1/admin/orders | 订单列表 |
| PUT | /api/v1/admin/orders/:id/status | 更新订单状态 |

## 核心设计

### 高并发处理

1. **多级缓存**: L1 本地缓存 (Go Cache) + L2 Redis 缓存
2. **限流**: 全局限流 + IP 限流 (令牌桶算法)
3. **秒杀优化**: 预热机制 + Lua 原子操作 + 令牌机制
4. **异步下单**: RabbitMQ 消息队列削峰

### 数据一致性

1. **库存补偿**: 定时任务比对 Redis 与 MySQL 库存
2. **幂等消费**: 基于订单 ID 做幂等校验
3. **失败重试**: 死信队列 + 延迟队列

### 可观测性

1. **日志**: Zap 结构化日志
2. **监控**: Prometheus 指标采集
3. **链路追踪**: Request ID 透传

## 压测

项目内置压测脚本，支持多种场景：

```bash
# 安装依赖
cd tests/load
npm install

# 基础读压测
node read_baseline.mjs

# 秒杀 spike 压测
node seckill_spike.mjs

# 混合读写压测
node shopping_mix.mjs
```

## 部署

详见 [DEPLOYMENT.md](./DEPLOYMENT.md)

推荐部署架构：
- Nginx (负载均衡) → Go Backend (多实例)
- 前端静态资源 (CDN)

## 许可证

MIT License