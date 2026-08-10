-- 第二阶段库存预约状态直接落在 ticket_order 行，避免订单确认事务额外插入一张递增主键表。
ALTER TABLE `ticket_order`
  ADD COLUMN `inventory_desired_state` varchar(16) NOT NULL DEFAULT 'none' AFTER `cancel_reason`,
  ADD COLUMN `inventory_applied_state` varchar(16) NOT NULL DEFAULT 'none' AFTER `inventory_desired_state`,
  ADD COLUMN `inventory_applied_at` datetime(3) DEFAULT NULL AFTER `inventory_applied_state`,
  ADD COLUMN `inventory_last_error` varchar(512) NOT NULL DEFAULT '' AFTER `inventory_applied_at`,
  ADD COLUMN `inventory_retry_count` int NOT NULL DEFAULT 0 AFTER `inventory_last_error`,
  ADD COLUMN `inventory_next_attempt_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) AFTER `inventory_retry_count`,
  ADD KEY `idx_ticket_order_inventory_work` (`inventory_applied_state`, `inventory_desired_state`, `inventory_next_attempt_at`, `id`);
