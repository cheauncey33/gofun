-- Payment provider boundary for the local sandbox.
-- This table stores payment orders/results, never a user's external balance.
CREATE TABLE IF NOT EXISTS `payment_transaction` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `payment_no` varchar(64) NOT NULL,
  `order_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `provider` varchar(32) NOT NULL,
  `provider_payment_id` varchar(128) NOT NULL,
  `amount_cents` bigint NOT NULL,
  `status` varchar(16) NOT NULL,
  `scenario` varchar(16) NOT NULL DEFAULT 'success',
  `failure_reason` varchar(256) NOT NULL DEFAULT '',
  `expires_at` datetime(3) NOT NULL,
  `paid_at` datetime(3) DEFAULT NULL,
  `refunded_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_payment_transaction_payment_no` (`payment_no`),
  KEY `idx_payment_transaction_order_id` (`order_id`),
  KEY `idx_payment_transaction_user_id` (`user_id`),
  KEY `idx_payment_transaction_status` (`status`),
  KEY `idx_payment_transaction_expires_at` (`expires_at`),
  CONSTRAINT `fk_payment_transaction_order` FOREIGN KEY (`order_id`) REFERENCES `ticket_order` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
CREATE TABLE IF NOT EXISTS `payment_callback` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `provider_event_id` varchar(128) NOT NULL,
  `payment_no` varchar(64) NOT NULL,
  `provider` varchar(32) NOT NULL,
  `status` varchar(16) NOT NULL,
  `amount_cents` bigint NOT NULL,
  `payload` json NOT NULL,
  `processed_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_payment_callback_provider_event_id` (`provider_event_id`),
  KEY `idx_payment_callback_payment_no` (`payment_no`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
