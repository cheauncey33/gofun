-- 账号绑定观演人；订单观演人增加稳定证件键，用于同一证件同一场只能买一张。

CREATE TABLE IF NOT EXISTS `user_attendee` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `user_id` bigint NOT NULL,
  `name` varchar(64) NOT NULL,
  `id_type` varchar(16) NOT NULL DEFAULT 'id_card',
  `id_number_masked` varchar(32) NOT NULL,
  `id_number_cipher` varchar(512) NOT NULL,
  `identity_key` varchar(64) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_attendee_identity` (`user_id`, `identity_key`),
  KEY `idx_user_attendee_delete_time` (`delete_time`),
  KEY `idx_user_attendee_user_id` (`user_id`),
  CONSTRAINT `fk_user_attendee_user` FOREIGN KEY (`user_id`) REFERENCES `user` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
ALTER TABLE `ticket_order_attendee`
  ADD COLUMN `identity_key` varchar(64) NOT NULL DEFAULT '' AFTER `id_number_hash`;
-- statement
ALTER TABLE `ticket_order_attendee`
  ADD KEY `idx_ticket_order_attendee_identity_key` (`identity_key`);
