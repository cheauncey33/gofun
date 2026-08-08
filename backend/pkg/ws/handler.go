package ws

import (
	"gofun/common"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type Handler struct {
	hub      *Hub
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, allowedOrigins ...string) *Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return &Handler{
		hub: hub,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return originAllowed(r, allowed)
			},
		},
	}
}

func (h *Handler) Handle(c *gin.Context) {
	claims, err := common.ParseToken(c.Query("token"))
	if err != nil || claims.TokenType != "access" || claims.UserID <= 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 40101, "msg": "WebSocket 鉴权失败"})
		return
	}
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &client{
		hub:    h.hub,
		conn:   conn,
		userID: claims.UserID,
		send:   make(chan []byte, 32),
	}
	h.hub.register <- client
	go client.writePump()
	client.readPump()
}

func originAllowed(r *http.Request, allowed map[string]struct{}) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err == nil && parsed.Host == r.Host {
		return true
	}
	_, ok := allowed[strings.TrimRight(origin, "/")]
	return ok
}
