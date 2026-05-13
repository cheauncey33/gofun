package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

func StartOrderConsumer(ctx context.Context, cfg config.OrderConsumerConfig) {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.PrefetchCount <= 0 {
		cfg.PrefetchCount = 5
	}

	log.Printf("order consumers starting: workers=%d prefetch=%d", cfg.WorkerCount, cfg.PrefetchCount)

	var wg sync.WaitGroup
	for i := 0; i < cfg.WorkerCount; i++ {
		workerID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			runOrderConsumerWorker(ctx, workerID, cfg.PrefetchCount)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	log.Println("all order consumers exited")
}

func runOrderConsumerWorker(ctx context.Context, workerID, prefetchCount int) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("order consumer worker %d received cancel, exiting", workerID)
			return
		default:
			if err := consumeLoop(ctx, workerID, prefetchCount); err != nil {
				log.Printf("order consumer worker %d error, reconnecting in 3s: %v\n", workerID, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
}

func consumeLoop(ctx context.Context, workerID, prefetchCount int) error {
	ch, err := common.NewMQChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Qos(prefetchCount, 0, false); err != nil {
		return fmt.Errorf("consumer worker %d qos error: %w", workerID, err)
	}

	msgs, err := ch.Consume(
		common.MQQueueName,
		fmt.Sprintf("order-worker-%d", workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consumer worker %d channel error: %w", workerID, err)
	}
	log.Printf("order consumer worker %d listening on %s", workerID, common.MQQueueName)

	for {
		select {
		case <-ctx.Done():
			log.Printf("order consumer worker %d exiting via context", workerID)
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("consumer worker %d MQ channel closed", workerID)
			}
			var msg OrderMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				log.Printf("order consumer worker %d parsing error: %v\n", workerID, err)
				d.Ack(false)
				continue
			}
			if err := ProcessOrderTask(msg); err != nil {
				log.Printf("order consumer worker %d order %d processing failed: %v\n", workerID, msg.OrderID, err)
				d.Nack(false, true)
			} else {
				d.Ack(false)
			}
		}
	}
}
