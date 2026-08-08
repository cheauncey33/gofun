package ws

import (
	"context"
	"encoding/json"
	"time"
)

type Event struct {
	Event      string    `json:"event"`
	OrderID    int64     `json:"order_id,string"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

type delivery struct {
	userID  int64
	payload []byte
}

type Hub struct {
	clients    map[int64]map[*client]struct{}
	register   chan *client
	unregister chan *client
	deliver    chan delivery
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[int64]map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		deliver:    make(chan delivery, 256),
	}
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for _, userClients := range h.clients {
				for client := range userClients {
					close(client.send)
					if client.conn != nil {
						_ = client.conn.Close()
					}
				}
			}
			return
		case registeredClient := <-h.register:
			if h.clients[registeredClient.userID] == nil {
				h.clients[registeredClient.userID] = make(map[*client]struct{})
			}
			h.clients[registeredClient.userID][registeredClient] = struct{}{}
		case unregisteredClient := <-h.unregister:
			h.remove(unregisteredClient)
		case item := <-h.deliver:
			for client := range h.clients[item.userID] {
				select {
				case client.send <- item.payload:
				default:
					h.remove(client)
				}
			}
		}
	}
}

func (h *Hub) Publish(userID int64, event Event) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case h.deliver <- delivery{userID: userID, payload: payload}:
	default:
		// 状态推送是提示通道；队列满时由订单查询和前端轮询兜底。
	}
}

func (h *Hub) remove(client *client) {
	userClients := h.clients[client.userID]
	if _, ok := userClients[client]; !ok {
		return
	}
	delete(userClients, client)
	close(client.send)
	if client.conn != nil {
		_ = client.conn.Close()
	}
	if len(userClients) == 0 {
		delete(h.clients, client.userID)
	}
}
