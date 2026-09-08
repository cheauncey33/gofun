package ws

import (
	"encoding/json"
	"errors"
	"gofun/common"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const wsAuthTimeout = 5 * time.Second

type Handler struct {
	hub      *Hub
	upgrader websocket.Upgrader
}

type wsAuthMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"`
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
	headerToken := accessTokenFromHeader(c.GetHeader("Authorization"))
	if headerToken != "" {
		userID, err := userIDFromAccessToken(headerToken)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 40101, "msg": "WebSocket 鉴权失败"})
			return
		}
		h.serve(c, userID)
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	userID, err := authenticateWSConn(conn)
	if err != nil {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "auth required"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
		return
	}
	h.runClient(conn, userID)
}

func (h *Handler) serve(c *gin.Context, userID int64) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.runClient(conn, userID)
}

func (h *Handler) runClient(conn *websocket.Conn, userID int64) {
	client := &client{
		hub:    h.hub,
		conn:   conn,
		userID: userID,
		send:   make(chan []byte, 32),
	}
	h.hub.register <- client
	go client.writePump()
	client.readPump()
}

func authenticateWSConn(conn *websocket.Conn) (int64, error) {
	_ = conn.SetReadDeadline(time.Now().Add(wsAuthTimeout))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		return 0, err
	}
	token, err := parseWSAuthMessage(payload)
	if err != nil {
		return 0, err
	}
	userID, err := userIDFromAccessToken(token)
	if err != nil {
		return 0, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return userID, nil
}

func parseWSAuthMessage(raw []byte) (string, error) {
	var msg wsAuthMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Type), "auth") {
		return "", errors.New("invalid auth message")
	}
	token := strings.TrimSpace(msg.Token)
	if token == "" {
		return "", errors.New("missing token")
	}
	return token, nil
}

func accessTokenFromHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return raw
}

func userIDFromAccessToken(token string) (int64, error) {
	claims, err := common.ParseToken(token)
	if err != nil || claims == nil || claims.UserID <= 0 {
		return 0, errors.New("invalid token")
	}
	if claims.TokenType != "" && claims.TokenType != "access" {
		return 0, errors.New("invalid token type")
	}
	return claims.UserID, nil
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
