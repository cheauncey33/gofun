-- 同访客漏斗：按自然日保留“访客-活动-阶段”事实，查询时跨天去重并求阶段交集。
CREATE TABLE IF NOT EXISTS `funnel_visitor_daily` (
  `event_id` bigint NOT NULL,
  `organizer_id` bigint NOT NULL,
  `day` date NOT NULL,
  `visitor_key` varchar(64) NOT NULL,
  `stage` varchar(16) NOT NULL,
  `hits` bigint NOT NULL DEFAULT 1,
  `first_at` datetime(3) NOT NULL,
  `last_at` datetime(3) NOT NULL,
  PRIMARY KEY (`event_id`, `day`, `visitor_key`, `stage`),
  KEY `idx_funnel_visitor_org_day_stage` (`organizer_id`, `day`, `stage`),
  KEY `idx_funnel_visitor_event_day_stage` (`event_id`, `day`, `stage`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
ALTER TABLE `ticket_order`
  ADD COLUMN `funnel_visitor_key` varchar(64) NOT NULL DEFAULT '' AFTER `request_id`;
-- statement
ALTER TABLE `waitlist_entry`
  ADD COLUMN `funnel_visitor_key` varchar(64) NOT NULL DEFAULT '' AFTER `request_id`;
