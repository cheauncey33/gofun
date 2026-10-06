ALTER TABLE `ticket_order`
  ADD KEY `idx_ticket_order_timeout_scan` (`status`, `expires_at`, `id`);
