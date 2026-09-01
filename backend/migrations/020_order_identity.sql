ALTER TABLE `ticket_order`
  ADD COLUMN `request_hash` varchar(64) NOT NULL DEFAULT '' AFTER `idempotency_key`;
-- statement
ALTER TABLE `ticket_order_outbox`
  ADD COLUMN `event_type` varchar(64) NOT NULL DEFAULT 'ticket.order.finalize' AFTER `order_id`;
-- statement
CREATE TABLE IF NOT EXISTS `ticket_order_consumer_inbox` (
  `consumer_name` varchar(64) NOT NULL,
  `event_id` bigint NOT NULL,
  `order_id` bigint NOT NULL,
  `create_time` datetime(3) NOT NULL,
  PRIMARY KEY (`consumer_name`, `event_id`),
  KEY `idx_ticket_order_consumer_inbox_order_id` (`order_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
