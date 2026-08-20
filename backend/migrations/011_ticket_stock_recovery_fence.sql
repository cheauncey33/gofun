-- 订单事务和库存恢复任务通过 order_id 唯一键串行化，
-- 避免恢复任务查不到未提交订单后提前归还 Redis 库存。
CREATE TABLE `ticket_stock_recovery_fence` (
  `order_id` bigint NOT NULL,
  `owner` varchar(16) NOT NULL,
  `create_time` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`order_id`),
  KEY `idx_ticket_stock_recovery_fence_owner_time` (`owner`, `create_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
