-- 固定场馆厅图：物理座位归属可复用厅图，场次座位继续作为独立库存事实。
CREATE TABLE IF NOT EXISTS `hall` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  `venue_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `status` varchar(16) NOT NULL DEFAULT 'active',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_hall_venue_name` (`venue_id`, `name`),
  KEY `idx_hall_delete_time` (`delete_time`),
  KEY `idx_hall_status` (`status`),
  CONSTRAINT `fk_hall_venue` FOREIGN KEY (`venue_id`) REFERENCES `venue` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
ALTER TABLE `seat_layout`
  ADD COLUMN `hall_id` bigint DEFAULT NULL AFTER `event_id`,
  ADD COLUMN `version` int NOT NULL DEFAULT 1 AFTER `hall_id`,
  ADD COLUMN `status` varchar(16) NOT NULL DEFAULT 'draft' AFTER `version`,
  ADD COLUMN `published_at` datetime(3) DEFAULT NULL AFTER `col_count`,
  ADD KEY `idx_seat_layout_hall_id` (`hall_id`),
  ADD KEY `idx_seat_layout_status` (`status`),
  ADD UNIQUE KEY `uk_hall_layout_version` (`hall_id`, `version`),
  ADD CONSTRAINT `fk_seat_layout_hall` FOREIGN KEY (`hall_id`) REFERENCES `hall` (`id`),
  MODIFY COLUMN `event_id` bigint DEFAULT NULL;
-- statement
ALTER TABLE `seat`
  DROP FOREIGN KEY `fk_seat_ticket_tier`,
  MODIFY COLUMN `ticket_tier_id` bigint DEFAULT NULL,
  ADD COLUMN `zone_key` varchar(64) NOT NULL DEFAULT 'general' AFTER `ticket_tier_id`,
  ADD KEY `idx_seat_zone_key` (`zone_key`);
-- statement
ALTER TABLE `event_session`
  ADD COLUMN `hall_id` bigint DEFAULT NULL AFTER `venue_id`,
  ADD COLUMN `seat_layout_id` bigint DEFAULT NULL AFTER `hall_id`,
  ADD KEY `idx_event_session_hall_id` (`hall_id`),
  ADD KEY `idx_event_session_seat_layout_id` (`seat_layout_id`),
  ADD CONSTRAINT `fk_event_session_hall` FOREIGN KEY (`hall_id`) REFERENCES `hall` (`id`),
  ADD CONSTRAINT `fk_event_session_layout` FOREIGN KEY (`seat_layout_id`) REFERENCES `seat_layout` (`id`);
-- statement
INSERT INTO `hall` (`venue_id`, `name`, `status`, `create_time`, `update_time`)
SELECT DISTINCT es.venue_id, CONCAT('历史厅-', sl.id), 'active', NOW(3), NOW(3)
FROM seat_layout sl
JOIN event_session es ON es.event_id = sl.event_id AND es.delete_time IS NULL
WHERE sl.hall_id IS NULL;
-- statement
UPDATE seat_layout sl
JOIN event_session es ON es.event_id = sl.event_id AND es.delete_time IS NULL
JOIN hall h ON h.venue_id = es.venue_id AND h.name = CONCAT('历史厅-', sl.id)
SET sl.hall_id = h.id, sl.status = 'published', sl.published_at = COALESCE(sl.published_at, NOW(3))
WHERE sl.hall_id IS NULL;
-- statement
UPDATE event_session es
JOIN seat_layout sl ON sl.event_id = es.event_id
SET es.hall_id = sl.hall_id, es.seat_layout_id = sl.id
WHERE es.seat_layout_id IS NULL;
