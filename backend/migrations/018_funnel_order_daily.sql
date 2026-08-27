-- 漏斗订单按日汇总：7/30 天查询只扫日表，不扫 ticket_order 明细。
CREATE TABLE IF NOT EXISTS `funnel_order_daily` (
  `event_id` bigint NOT NULL,
  `organizer_id` bigint NOT NULL,
  `day` date NOT NULL,
  `source` varchar(16) NOT NULL,
  `submitted` bigint NOT NULL DEFAULT 0,
  `paid` bigint NOT NULL DEFAULT 0,
  `refunded` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`event_id`, `day`, `source`),
  KEY `idx_funnel_order_org_day` (`organizer_id`, `day`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement
ALTER TABLE `ticket_order`
  ADD KEY `idx_ticket_order_org_status_ctime` (`organizer_id`, `status`, `create_time`);
-- statement
ALTER TABLE `waitlist_entry`
  ADD KEY `idx_waitlist_entry_org_status_ctime` (`organizer_id`, `status`, `create_time`);
-- statement
ALTER TABLE `admission_ticket`
  ADD KEY `idx_admission_ticket_org_status_used` (`organizer_id`, `status`, `used_at`);
