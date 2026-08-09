# Gofun 前端

这是 Gofun 活动票务前端，使用 Vue 3、Vite、Element Plus、Vue Router 和 Axios。

## 本地运行

在 `frontend/` 目录执行：

```powershell
npm.cmd install
npm.cmd run dev
```

开发服务器默认通过 Vite proxy 将 `/api` 转发到
`http://127.0.0.1:8080`。后端、MySQL、Redis 和 RabbitMQ 需要单独启动。

## 构建验证

```powershell
npm.cmd run build
```

主要页面包括活动目录与详情、购物车/结算、沙箱收银台、订单详情、电子票和
主办方运营台。订单状态通过 API 轮询与 WebSocket 更新；票务 ID 按后端约定以
JSON 字符串接收。
