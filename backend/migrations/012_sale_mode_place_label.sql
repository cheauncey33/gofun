-- 活动卖法：counter 计数（展览/演唱会），seated 选座（下一批）。
-- 区内编号写在电子票 place_label，不参与库存扣减。
ALTER TABLE `event`
  ADD COLUMN `sale_mode` varchar(16) NOT NULL DEFAULT 'counter' AFTER `max_tickets_per_order`;
-- statement
ALTER TABLE `ticket_tier`
  ADD COLUMN `assign_place_no` tinyint(1) NOT NULL DEFAULT 1 AFTER `purchase_limit`,
  ADD COLUMN `place_seq` bigint NOT NULL DEFAULT 0 AFTER `assign_place_no`;
-- statement
ALTER TABLE `admission_ticket`
  ADD COLUMN `place_label` varchar(64) NOT NULL DEFAULT '' AFTER `ticket_tier_id`;
