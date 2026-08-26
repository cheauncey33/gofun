-- 选座库存与计数库存分开：厅图/座位是模板，场次座位才是可售库存。
-- session_seat 一座一行，状态 available / held / sold；不走票档 remaining 计数，也不分桶。

CREATE TABLE IF NOT EXISTS `seat_layout` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `event_id` bigint NOT NULL,
  `name` varchar(64) NOT NULL DEFAULT '',
  `row_count` int NOT NULL,
  `col_count` int NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_seat_layout_event` (`event_id`),
  KEY `idx_seat_layout_delete_time` (`delete_time`),
  CONSTRAINT `fk_seat_layout_event` FOREIGN KEY (`event_id`) REFERENCES `event` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
CREATE TABLE IF NOT EXISTS `seat` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `layout_id` bigint NOT NULL,
  `ticket_tier_id` bigint NOT NULL,
  `row_no` int NOT NULL,
  `col_no` int NOT NULL,
  `label` varchar(32) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_seat_layout_cell` (`layout_id`, `row_no`, `col_no`),
  KEY `idx_seat_delete_time` (`delete_time`),
  KEY `idx_seat_ticket_tier_id` (`ticket_tier_id`),
  CONSTRAINT `fk_seat_layout` FOREIGN KEY (`layout_id`) REFERENCES `seat_layout` (`id`),
  CONSTRAINT `fk_seat_ticket_tier` FOREIGN KEY (`ticket_tier_id`) REFERENCES `ticket_tier` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
CREATE TABLE IF NOT EXISTS `session_seat` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `session_id` bigint NOT NULL,
  `seat_id` bigint NOT NULL,
  `ticket_tier_id` bigint NOT NULL,
  `order_id` bigint DEFAULT NULL,
  `status` varchar(16) NOT NULL DEFAULT 'available',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_session_seat` (`session_id`, `seat_id`),
  KEY `idx_session_seat_delete_time` (`delete_time`),
  KEY `idx_session_seat_ticket_tier_id` (`ticket_tier_id`),
  KEY `idx_session_seat_order_id` (`order_id`),
  KEY `idx_session_seat_session_status` (`session_id`, `status`),
  CONSTRAINT `fk_session_seat_session` FOREIGN KEY (`session_id`) REFERENCES `event_session` (`id`),
  CONSTRAINT `fk_session_seat_seat` FOREIGN KEY (`seat_id`) REFERENCES `seat` (`id`),
  CONSTRAINT `fk_session_seat_tier` FOREIGN KEY (`ticket_tier_id`) REFERENCES `ticket_tier` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
