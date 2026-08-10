-- 删除历史 Batch Saga 在 ticket_order 上留下的状态、重试和 lease 字段。
-- 历史 inventory_operation 不再由应用创建或使用；当前库存统一为主库分桶事务。
ALTER TABLE `ticket_order`
  DROP INDEX `idx_ticket_order_inventory_lease`,
  DROP INDEX `idx_ticket_order_inventory_work`;
-- statement
ALTER TABLE `ticket_order`
  DROP COLUMN `inventory_processing_deadline`,
  DROP COLUMN `inventory_processing_at`,
  DROP COLUMN `inventory_worker_token`,
  DROP COLUMN `inventory_next_attempt_at`,
  DROP COLUMN `inventory_retry_count`,
  DROP COLUMN `inventory_last_error`,
  DROP COLUMN `inventory_applied_at`,
  DROP COLUMN `inventory_applied_state`,
  DROP COLUMN `inventory_desired_state`;
