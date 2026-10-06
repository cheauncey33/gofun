//go:build integration

package service

import (
	"gofun/models"
	"testing"
)

func TestOutboxPublishedBatchKeepsAttemptsAndStatus(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	rows := []models.TicketOrderOutbox{
		{ID: env.svc.node.Generate().Int64(), OrderID: env.svc.node.Generate().Int64(), EventType: ticketOrderFinalizeEventType, Payload: "{}", Status: models.TicketOrderOutboxPublishing, Attempts: 2, LastError: "previous failure"},
		{ID: env.svc.node.Generate().Int64(), OrderID: env.svc.node.Generate().Int64(), EventType: ticketOrderFinalizeEventType, Payload: "{}", Status: models.TicketOrderOutboxPublishing, Attempts: 5},
		{ID: env.svc.node.Generate().Int64(), OrderID: env.svc.node.Generate().Int64(), EventType: ticketOrderFinalizeEventType, Payload: "{}", Status: models.TicketOrderOutboxPending, Attempts: 1},
	}
	ids := []int64{rows[0].ID, rows[1].ID, rows[2].ID}
	if err := env.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.db.Where("id IN ?", ids).Delete(&models.TicketOrderOutbox{}) })
	for i := 0; i < 2; i++ {
		if err := env.svc.markOutboxPublishedBatch(t.Context(), ids); err != nil {
			t.Fatal(err)
		}
	}
	for i, original := range rows {
		var got models.TicketOrderOutbox
		if err := env.db.First(&got, original.ID).Error; err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if got.Status != original.Status || got.Attempts != original.Attempts || got.PublishedAt != nil {
				t.Fatalf("pending row changed: %+v", got)
			}
			continue
		}
		if got.Status != models.TicketOrderOutboxPublished || got.Attempts != original.Attempts+1 || got.LastError != "" || got.PublishedAt == nil {
			t.Fatalf("unexpected published row: %+v", got)
		}
	}
}
