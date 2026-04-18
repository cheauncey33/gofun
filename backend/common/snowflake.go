package common

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

var Node *snowflake.Node

func InitSnowFlake() {
	// 初始化一个节点，ID 为 1
	// (在真实的分布式集群中，每台机器的 node ID 需要不同，范围是 0 到 1023)
	node, err := snowflake.NewNode(1)
	if err != nil {
		panic(fmt.Errorf("snowflake init error: %v", err))
	}
	Node = node
	fmt.Println("snowflake node started")
}
