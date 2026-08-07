-- 票档 / 抢票库存分桶：热点行打散；订单记录预扣桶号以便回滚对齐。

CREATE TABLE IF NOT EXISTS `ticket_tier_bucket` (
  `tier_id` bigint NOT NULL,
  `bucket_no` int NOT NULL,
  `remaining_quota` int NOT NULL,
  `sold_count` bigint NOT NULL DEFAULT 0,
  `version` bigint NOT NULL DEFAULT 0,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`tier_id`, `bucket_no`),
  KEY `idx_ticket_tier_bucket_remaining` (`remaining_quota`),
  CONSTRAINT `fk_ticket_tier_bucket_tier` FOREIGN KEY (`tier_id`) REFERENCES `ticket_tier` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
CREATE TABLE IF NOT EXISTS `rush_campaign_bucket` (
  `campaign_id` bigint NOT NULL,
  `bucket_no` int NOT NULL,
  `remaining_quota` int NOT NULL,
  `version` bigint NOT NULL DEFAULT 0,
  `update_time` datetime(3) DEFAULT NULL,
  `create_time` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`campaign_id`, `bucket_no`),
  KEY `idx_rush_campaign_bucket_remaining` (`remaining_quota`),
  CONSTRAINT `fk_rush_campaign_bucket_campaign` FOREIGN KEY (`campaign_id`) REFERENCES `rush_sale_campaign` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
ALTER TABLE `ticket_order`
  ADD COLUMN `stock_bucket_no` int DEFAULT NULL AFTER `request_id`,
  ADD COLUMN `rush_bucket_no` int DEFAULT NULL AFTER `stock_bucket_no`;
