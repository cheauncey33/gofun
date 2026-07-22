CREATE TABLE IF NOT EXISTS ticket_order_outbox (
  id BIGINT NOT NULL PRIMARY KEY,
  order_id BIGINT NOT NULL,
  payload JSON NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  attempts INT NOT NULL DEFAULT 0,
  last_error VARCHAR(512) NOT NULL DEFAULT '',
  create_time DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  published_at DATETIME(3) NULL,
  INDEX idx_outbox_status_create (status, create_time),
  INDEX idx_outbox_order (order_id)
);
