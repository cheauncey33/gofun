-- 主办方自助申请审核备注；活动上架需管理员审批。
ALTER TABLE `organizer`
  ADD COLUMN `audit_note` varchar(256) NOT NULL DEFAULT '' AFTER `audit_status`;
-- statement
ALTER TABLE `event`
  ADD COLUMN `review_note` varchar(256) NOT NULL DEFAULT '' AFTER `published_at`;
