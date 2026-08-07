package service

import (
	"gofun/models"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeCommentContent(t *testing.T) {
	t.Parallel()

	if _, err := normalizeCommentContent(" "); !errors.Is(err, ErrCommentInvalid) {
		t.Fatalf("expected invalid for blank, got %v", err)
	}
	if _, err := normalizeCommentContent("a"); !errors.Is(err, ErrCommentInvalid) {
		t.Fatalf("expected invalid for short content, got %v", err)
	}
	long := strings.Repeat("汉", 501)
	if _, err := normalizeCommentContent(long); !errors.Is(err, ErrCommentInvalid) {
		t.Fatalf("expected invalid for long content, got %v", err)
	}

	got, err := normalizeCommentContent("  开售前约人  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "开售前约人" {
		t.Fatalf("unexpected normalized content: %q", got)
	}
}

func TestToCommentViewMarksOwner(t *testing.T) {
	t.Parallel()

	item := models.EventComment{
		Base:      models.Base{ID: 11, CreateTime: time.Unix(1_700_000_000, 0)},
		EventID:   22,
		UserID:    33,
		Content:   "想看哪场",
		LikeCount: 2,
		User:      &models.User{Username: "alice"},
	}
	view := toCommentView(item, 33)
	if !view.IsOwner || view.Username != "alice" || view.ID != "11" {
		t.Fatalf("unexpected owner view: %#v", view)
	}
	other := toCommentView(item, 99)
	if other.IsOwner {
		t.Fatal("expected non-owner")
	}
}
