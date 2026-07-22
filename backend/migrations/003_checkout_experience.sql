-- 购票联系人、实名观演人和购票须知确认快照。

ALTER TABLE `ticket_order`
  ADD COLUMN `contact_name` varchar(64) NOT NULL DEFAULT '' AFTER `total_amount_cents`,
  ADD COLUMN `contact_phone` varchar(20) NOT NULL DEFAULT '' AFTER `contact_name`,
  ADD COLUMN `real_name_required` tinyint(1) NOT NULL DEFAULT 0 AFTER `contact_phone`,
  ADD COLUMN `purchase_notice_version` varchar(32) NOT NULL DEFAULT '' AFTER `real_name_required`,
  ADD COLUMN `terms_accepted_at` datetime(3) DEFAULT NULL AFTER `purchase_notice_version`;
-- statement
CREATE TABLE IF NOT EXISTS `ticket_order_attendee` (
  `id` bigint NOT NULL,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `order_id` bigint NOT NULL,
  `sequence_no` int NOT NULL,
  `name` varchar(64) NOT NULL,
  `id_type` varchar(16) NOT NULL,
  `id_number_masked` varchar(32) NOT NULL,
  `id_number_hash` varchar(64) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_ticket_order_attendee` (`order_id`,`sequence_no`),
  KEY `idx_ticket_order_attendee_delete_time` (`delete_time`),
  KEY `idx_ticket_order_attendee_order_id` (`order_id`),
  KEY `idx_ticket_order_attendee_id_number_hash` (`id_number_hash`),
  CONSTRAINT `fk_ticket_order_attendee_order` FOREIGN KEY (`order_id`) REFERENCES `ticket_order` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
