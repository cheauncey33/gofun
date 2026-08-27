-- 候补：已付款排队，不占公开库存。退票先进待派发，按 FIFO 派给队头。
ALTER TABLE `ticket_tier`
  ADD COLUMN `waitlist_pending` int NOT NULL DEFAULT 0 AFTER `sold_count`;
-- statement
ALTER TABLE `payment_transaction`
  DROP FOREIGN KEY `fk_payment_transaction_order`;
-- statement
ALTER TABLE `payment_transaction`
  ADD COLUMN `waitlist_id` bigint NOT NULL DEFAULT 0 AFTER `order_id`;
-- statement
ALTER TABLE `payment_transaction`
  ADD KEY `idx_payment_transaction_waitlist_id` (`waitlist_id`);
-- statement
CREATE TABLE IF NOT EXISTS `waitlist_entry` (
  `id` bigint NOT NULL,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `waitlist_no` varchar(32) NOT NULL,
  `user_id` bigint NOT NULL,
  `organizer_id` bigint NOT NULL,
  `event_id` bigint NOT NULL,
  `session_id` bigint NOT NULL,
  `ticket_tier_id` bigint NOT NULL,
  `quantity` int NOT NULL,
  `amount_cents` bigint NOT NULL,
  `status` varchar(24) NOT NULL,
  `contact_name` varchar(64) NOT NULL,
  `contact_phone` varchar(20) NOT NULL,
  `real_name_required` tinyint(1) NOT NULL DEFAULT 0,
  `purchase_notice_version` varchar(32) NOT NULL,
  `event_title_snapshot` varchar(160) NOT NULL,
  `session_starts_at_snapshot` datetime(3) NOT NULL,
  `venue_name_snapshot` varchar(128) NOT NULL,
  `venue_address_snapshot` varchar(256) NOT NULL,
  `tier_name_snapshot` varchar(64) NOT NULL,
  `idempotency_key` varchar(64) NOT NULL,
  `request_id` varchar(64) DEFAULT NULL,
  `expires_at` datetime(3) NOT NULL,
  `paid_at` datetime(3) DEFAULT NULL,
  `fulfilled_order_id` bigint DEFAULT NULL,
  `cancelled_at` datetime(3) DEFAULT NULL,
  `cancel_reason` varchar(256) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_waitlist_entry_waitlist_no` (`waitlist_no`),
  UNIQUE KEY `uk_waitlist_idempotency` (`user_id`,`idempotency_key`),
  KEY `idx_waitlist_entry_delete_time` (`delete_time`),
  KEY `idx_waitlist_tier_fifo` (`ticket_tier_id`,`status`,`id`),
  KEY `idx_waitlist_entry_user_id` (`user_id`),
  KEY `idx_waitlist_entry_event_id` (`event_id`),
  KEY `idx_waitlist_entry_expires_at` (`expires_at`),
  KEY `idx_waitlist_entry_fulfilled_order_id` (`fulfilled_order_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
CREATE TABLE IF NOT EXISTS `waitlist_attendee` (
  `id` bigint NOT NULL,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `waitlist_id` bigint NOT NULL,
  `sequence_no` int NOT NULL,
  `name` varchar(64) NOT NULL,
  `id_type` varchar(16) NOT NULL,
  `id_number_masked` varchar(32) NOT NULL,
  `id_number_hash` varchar(64) NOT NULL,
  `identity_key` varchar(64) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_waitlist_attendee` (`waitlist_id`,`sequence_no`),
  KEY `idx_waitlist_attendee_delete_time` (`delete_time`),
  KEY `idx_waitlist_attendee_waitlist_id` (`waitlist_id`),
  KEY `idx_waitlist_attendee_identity_key` (`identity_key`),
  CONSTRAINT `fk_waitlist_attendee_entry` FOREIGN KEY (`waitlist_id`) REFERENCES `waitlist_entry` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
