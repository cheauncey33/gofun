ALTER TABLE `ticket_order`
  ADD COLUMN `recovery_only` tinyint(1) NOT NULL DEFAULT 0;
-- statement
ALTER TABLE `ticket_order_outbox`
  ADD COLUMN `next_attempt_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD UNIQUE KEY `uk_outbox_order_event` (`order_id`, `event_type`),
  ADD KEY `idx_outbox_recovery_due` (`event_type`, `status`, `next_attempt_at`, `id`);
-- statement
INSERT INTO `ticket_order`
 (`id`, `order_no`, `user_id`, `organizer_id`, `event_id`, `session_id`,
  `order_source`, `status`, `payment_status`, `total_amount_cents`,
  `idempotency_key`, `expires_at`, `create_time`, `update_time`, `delete_time`, `recovery_only`)
SELECT f.order_id, CONCAT('REC', f.order_id), 0, 0, 0, 0,
 'normal', 'failed', 'unpaid', 0, CONCAT('recovery:', f.order_id),
 f.create_time, f.create_time, f.create_time, f.create_time, 1
FROM `ticket_stock_recovery_fence` f
LEFT JOIN `ticket_order` o ON o.id = f.order_id
WHERE f.owner = 'recovery' AND o.id IS NULL;
-- statement
DROP TABLE `ticket_stock_recovery_fence`;
