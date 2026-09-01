package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	ticketOrderOperationNormal = "ticket.create"
	ticketOrderOperationRush   = "rush.execute"
)

// orderRequestHash binds an idempotency key to one exact request payload.
func orderRequestHash(operation string, payload interface{}) string {
	raw, _ := json.Marshal(struct {
		Operation string      `json:"operation"`
		Payload   interface{} `json:"payload"`
	}{Operation: operation, Payload: payload})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
