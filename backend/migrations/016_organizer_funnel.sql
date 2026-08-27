-- 主办方漏斗：前台浏览/详情/下单页按日去重计数。订单与候补转化仍查业务表。
CREATE TABLE IF NOT EXISTS `funnel_daily` (
  `event_id` bigint NOT NULL,
  `organizer_id` bigint NOT NULL,
  `day` date NOT NULL,
  `stage` varchar(16) NOT NULL,
  `hits` bigint NOT NULL DEFAULT 0,
  `uniques` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`event_id`, `day`, `stage`),
  KEY `idx_funnel_org_day` (`organizer_id`, `day`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
