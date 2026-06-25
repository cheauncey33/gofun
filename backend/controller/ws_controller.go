package controller

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/pkg/ws"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WSController 处理 WebSocket 连接的建立与鉴权。
type WSController struct {
	hub      *ws.Hub
	upgrader websocket.Upgrader
}

// NewWSController 构造 WebSocket 控制器。
func NewWSController(hub *ws.Hub) *WSController {
	return &WSController{
		hub: hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// 跨域校验交由上层 CORS 与 token 鉴权把关，这里放行握手请求。
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Handle 升级 HTTP 连接为 WebSocket。
// 浏览器原生 WebSocket 无法自定义请求头，因此 access token 通过查询参数 ?token= 传入并校验。
func (ctrl *WSController) Handle(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "缺少token"})
		return
	}
	claims, err := common.ParseToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "token无效或已过期"})
		return
	}

	conn, err := ctrl.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade 失败时 gin 已无法再写响应，记录由库内部完成，这里直接返回。
		return
	}
	ctrl.hub.ServeWS(conn, claims.UserID)
}
