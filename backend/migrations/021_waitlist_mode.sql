-- 候补按实际付款成功时间排队；ticket_tier.status 新增 waitlist 值，无需变更 varchar 结构。
ALTER TABLE `waitlist_entry`
  ADD KEY `idx_waitlist_tier_status_paid` (`ticket_tier_id`, `status`, `paid_at`, `id`);
-- statement
-- 部署前已售罄的计数票档也进入候补模式；选座票档不支持候补，保持 sold_out。
UPDATE `ticket_tier` AS tier
JOIN `event_session` AS session ON session.id = tier.session_id
JOIN `event` AS event_row ON event_row.id = session.event_id
SET tier.status = 'waitlist'
WHERE tier.status = 'sold_out'
  AND tier.delete_time IS NULL
  AND event_row.sale_mode = 'counter';
