package service

import "testing"

func TestPublicRedisExpectedSubtractsWaitlistPending(t *testing.T) {
	t.Parallel()
	if got := publicRedisExpected(5, 5, 0, false); got != 0 {
		t.Fatalf("all earmarked public=%d, want 0", got)
	}
	if got := publicRedisExpected(7, 5, 0, false); got != 2 {
		t.Fatalf("public leftover=%d, want 2", got)
	}
	if got := publicRedisExpected(7, 5, 1, false); got != 1 {
		t.Fatalf("public minus queued=%d, want 1", got)
	}
	if got := publicRedisExpected(7, 5, 0, true); got != 0 {
		t.Fatalf("bucket mode with pending must zero public redis, got %d", got)
	}
	if got := publicRedisExpected(7, 0, 2, true); got != 5 {
		t.Fatalf("bucket mode without pending=%d, want 5", got)
	}
}

func TestPlanWaitlistFulfillmentStrictFIFO(t *testing.T) {
	t.Parallel()
	fulfilled, leftover, keep := planWaitlistFulfillment(3, []int{2, 1})
	if len(fulfilled) != 2 || leftover != 0 || keep {
		t.Fatalf("full fill indexes=%v leftover=%d keep=%v", fulfilled, leftover, keep)
	}

	fulfilled, leftover, keep = planWaitlistFulfillment(1, []int{2, 1})
	if len(fulfilled) != 0 || leftover != 1 || !keep {
		t.Fatalf("head wants 2 with 1 pending must wait, indexes=%v leftover=%d keep=%v", fulfilled, leftover, keep)
	}

	fulfilled, leftover, keep = planWaitlistFulfillment(3, []int{2})
	if len(fulfilled) != 1 || leftover != 1 || keep {
		t.Fatalf("queue empty leftover should go public, indexes=%v leftover=%d keep=%v", fulfilled, leftover, keep)
	}
}

func TestShouldDivertReleasedQuota(t *testing.T) {
	t.Parallel()
	if !shouldDivertReleasedQuota(1, false, false) {
		t.Fatal("queued waitlist must divert refunded stock")
	}
	if shouldDivertReleasedQuota(0, false, false) {
		t.Fatal("empty queue should return stock to public redis")
	}
	if shouldDivertReleasedQuota(2, true, false) {
		t.Fatal("rush orders must not divert to waitlist")
	}
	if shouldDivertReleasedQuota(2, false, true) {
		t.Fatal("seated restore already skipped redis")
	}
}
