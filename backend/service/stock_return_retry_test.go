package service

import (
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gofun/config"
	"sync"
	"testing"
)

func TestStockReturnConcurrentReplayIsIdempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	executor := &stockReturnRetry{rdb: rdb, cfg: NewStockReturnRetrySettings(config.InventoryConfig{})}
	task := stockReturnTask{Token: stockReturnToken(5001, stockReturnKindTicket), StockKey: "stock:1", Quantity: 2}
	mr.Set(task.StockKey, "3")
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := executor.execute(t.Context(), task)
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if value, err := rdb.Get(t.Context(), task.StockKey).Int(); err != nil || value != 5 {
		t.Fatalf("stock=%d err=%v", value, err)
	}
}

func TestStockReturnFailedIncrementCanRetry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	executor := &stockReturnRetry{rdb: rdb, cfg: NewStockReturnRetrySettings(config.InventoryConfig{})}
	task := stockReturnTask{Token: stockReturnToken(5002, stockReturnKindTicket), StockKey: "stock:2", Quantity: 2}
	mr.HSet(task.StockKey, "bad", "type")
	if _, err := executor.execute(t.Context(), task); err == nil {
		t.Fatal("expected wrongtype error")
	}
	if applied, _ := rdb.HExists(t.Context(), stockReturnTaskKey(task.Token), "applied").Result(); applied {
		t.Fatal("failed increment marked applied")
	}
	mr.Del(task.StockKey)
	mr.Set(task.StockKey, "3")
	if changed, err := executor.execute(t.Context(), task); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if value, err := rdb.Get(t.Context(), task.StockKey).Int(); err != nil || value != 5 {
		t.Fatalf("stock=%d err=%v", value, err)
	}
}
