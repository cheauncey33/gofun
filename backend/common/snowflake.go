package common

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

var Node *snowflake.Node

func InitSnowFlake(nodeID int64) {
	node, err := snowflake.NewNode(nodeID)
	if err != nil {
		panic(fmt.Errorf("snowflake init error: %v", err))
	}
	Node = node
	fmt.Println("snowflake node started")
}
