// Package ws 提供基于 WebSocket 的实时消息推送能力。
// Hub 以 userID 为粒度管理连接，支持同一用户多端（多标签页）同时在线，
// 业务侧通过 PushJSON 把订单状态变更等事件主动推送给指定用户。
package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait 单次写操作允许的最长时间
	writeWait = 10 * time.Second
	// pongWait 在该时间内未收到客户端 pong 则判定连接已死
	pongWait = 60 * time.Second
	// pingPeriod 服务端发送 ping 的周期，必须小于 pongWait
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize 客户端上行消息的最大长度（本系统客户端基本只读，限制很小即可）
	maxMessageSize = 512
	// sendBuffer 每个连接的发送缓冲，满了说明客户端消费过慢，丢弃以保护服务端
	sendBuffer = 16
)

// Client 表示一条已建立的 WebSocket 连接。
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	userID int64
	send   chan []byte
}

// Hub 维护所有在线连接，按 userID 分组。
type Hub struct {
	mu         sync.RWMutex
	clients    map[int64]map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
}

// NewHub 创建一个空的 Hub，需要配合 Run 在独立 goroutine 中启动。
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[int64]map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run 串行处理连接的注册与注销，避免对 clients map 的并发写竞争。
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			if h.clients[c.userID] == nil {
				h.clients[c.userID] = make(map[*Client]struct{})
			}
			h.clients[c.userID][c] = struct{}{}
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if set, ok := h.clients[c.userID]; ok {
				if _, ok := set[c]; ok {
					delete(set, c)
					close(c.send)
					if len(set) == 0 {
						delete(h.clients, c.userID)
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

// ServeWS 接管一条已升级完成的连接：注册到 Hub 并启动读写两个 pump。
func (h *Hub) ServeWS(conn *websocket.Conn, userID int64) {
	client := &Client{
		hub:    h,
		conn:   conn,
		userID: userID,
		send:   make(chan []byte, sendBuffer),
	}
	h.register <- client
	go client.writePump()
	go client.readPump()
}

// PushJSON 把 v 序列化为 JSON 并推送给 userID 的所有在线连接。
// 用户不在线时静默忽略（前端重新进入页面会通过 HTTP 拉到最新状态兜底）。
func (h *Hub) PushJSON(userID int64, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[ws] marshal push payload failed: %v", err)
		return
	}

	h.mu.RLock()
	set := h.clients[userID]
	targets := make([]*Client, 0, len(set))
	for c := range set {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- data:
		default:
			// 发送缓冲已满，说明该客户端消费过慢，丢弃本条以防阻塞推送方
			log.Printf("[ws] drop message for user %d: send buffer full", userID)
		}
	}
}

// readPump 持续读取客户端消息（本系统客户端只需保活，不发业务消息），
// 主要作用是处理 pong 心跳并在连接断开时触发注销。
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

// writePump 负责把 send 通道里的消息写给客户端，并周期性发送 ping 保活。
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub 关闭了该连接的发送通道
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
