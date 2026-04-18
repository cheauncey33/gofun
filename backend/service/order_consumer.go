package service

import (
	"WHU_Snack_GO/common"
	"context"
	"encoding/json"
	"fmt"
	"log"
)

func StartOrderConsumer(ctx context.Context) {
	msgs, err := common.MQChannel.Consume(
		"order_queue",
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		panic(fmt.Errorf("consumer starting error:%w", err))
	}
	fmt.Println("consumer starting success")

	for {
		select {
		case <-ctx.Done():
			log.Println("Heard  'cancel' from main, now exiting")
			return
		case d, ok := <-msgs:
			if !ok {
				log.Println("Can't get msgs, the MQ maybe closed unnormally")
				return
			}
			var msg OrderMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				//此条信息可能格式不对，解析失败 解析失败后需要Not ack 消耗掉这条信息
				log.Printf("Parsing fail:%v\n", err)
				d.Nack(false, false)
				continue
			}
			if err := ProcessOrderTask(msg); err != nil {
				log.Printf("订单处理失败：%v\n", err)
				d.Ack(false)
			} else {
				log.Printf("订单异步处理成功。\n")
				d.Ack(false)
			}
		}

	}
}
