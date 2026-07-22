CREATE TABLE IF NOT EXISTS `event_comment` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `event_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `content` varchar(500) NOT NULL,
  `like_count` bigint NOT NULL DEFAULT 0,
  `create_time` datetime(3) DEFAULT NULL,
  `update_time` datetime(3) DEFAULT NULL,
  `delete_time` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_event_comment_event_id` (`event_id`),
  KEY `idx_event_comment_user_id` (`user_id`),
  KEY `idx_event_comment_delete_time` (`delete_time`),
  KEY `idx_event_comment_event_created` (`event_id`, `create_time`),
  CONSTRAINT `fk_event_comment_event` FOREIGN KEY (`event_id`) REFERENCES `event` (`id`),
  CONSTRAINT `fk_event_comment_user` FOREIGN KEY (`user_id`) REFERENCES `user` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
