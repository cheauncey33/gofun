package service

import (
	"testing"
	"time"

	"gofun/models"
)

func TestEventFavoriteStatusOpenRush(t *testing.T) {
	now := time.Now()
	event := models.Event{Status: models.EventStatusPublished}
	sessions := []models.EventSession{{
		Status:       models.SessionStatusOnSale,
		SaleStartsAt: now.Add(-2 * time.Hour),
		SaleEndsAt:   now.Add(-time.Hour),
		EndsAt:       now.Add(2 * time.Hour),
	}}
	status, label := eventFavoriteStatus(event, sessions, true)
	if status != "live" || label != "抢票中" {
		t.Fatalf("open rush should be live, got %s %s", status, label)
	}
}

func TestEventFavoriteStatusSaleClosed(t *testing.T) {
	now := time.Now()
	event := models.Event{Status: models.EventStatusPublished}
	sessions := []models.EventSession{{
		Status:       models.SessionStatusOnSale,
		SaleStartsAt: now.Add(-2 * time.Hour),
		SaleEndsAt:   now.Add(-time.Hour),
		EndsAt:       now.Add(2 * time.Hour),
	}}
	status, label := eventFavoriteStatus(event, sessions, false)
	if status != "sale_closed" || label != "已停售" {
		t.Fatalf("closed sale should be sale_closed, got %s %s", status, label)
	}
}
