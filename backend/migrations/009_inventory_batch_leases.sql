-- 第三阶段库存批处理 lease：主库 claim 提交后不再持有 ticket_order 行锁。
ALTER TABLE `ticket_order`
  ADD COLUMN `inventory_worker_token` varchar(64) NOT NULL DEFAULT '' AFTER `inventory_next_attempt_at`,
  ADD COLUMN `inventory_processing_at` datetime(3) DEFAULT NULL AFTER `inventory_worker_token`,
  ADD COLUMN `inventory_processing_deadline` datetime(3) DEFAULT NULL AFTER `inventory_processing_at`,
  ADD KEY `idx_ticket_order_inventory_lease` (`inventory_worker_token`, `inventory_processing_deadline`, `id`);
