-- MeshOps P0 contract alignment. Apply after 001; business workers are not implemented.
-- Existing data must be reviewed before enabling services: NULL ownership/executor bindings
-- are deliberately not guessed. Application writes must supply these fields for new records.

ALTER TABLE entities
  ADD COLUMN owner_source_id VARCHAR(64) NULL COMMENT 'Authoritative source; NULL denies writes',
  ADD COLUMN source_generation BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN executor_id VARCHAR(128) NULL COMMENT 'Configured execution endpoint identity',
  ADD COLUMN task_catalog JSON NULL,
  ADD INDEX idx_owner_source (tenant_id, owner_source_id);

ALTER TABLE tasks
  ADD COLUMN executor_id VARCHAR(128) NULL,
  ADD COLUMN execution_key VARCHAR(128) NULL,
  ADD COLUMN request_hash CHAR(64) NULL COMMENT 'Canonical request digest for idempotency conflicts',
  ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE tasks SET execution_key = task_id WHERE execution_key IS NULL;
ALTER TABLE tasks ADD UNIQUE KEY uk_task_execution (tenant_id, execution_key);

ALTER TABLE outbox_events
  ADD COLUMN event_id VARCHAR(128) NULL,
  ADD COLUMN tenant_id VARCHAR(64) NULL,
  ADD COLUMN next_attempt_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  ADD COLUMN lease_owner VARCHAR(128) NULL,
  ADD COLUMN lease_until DATETIME(6) NULL;

UPDATE outbox_events SET event_id = CONCAT('legacy-outbox-', id) WHERE event_id IS NULL;
ALTER TABLE outbox_events
  ADD UNIQUE KEY uk_outbox_event (event_id),
  ADD INDEX idx_outbox_due (published_at, next_attempt_at);

-- Separate transport attempts from stable business execution identity.
ALTER TABLE task_dispatches
  ADD COLUMN tenant_id VARCHAR(64) NULL,
  ADD COLUMN execution_key VARCHAR(128) NULL,
  ADD COLUMN command_id VARCHAR(128) NULL,
  ADD COLUMN command_kind ENUM('execute', 'cancel') NOT NULL DEFAULT 'execute',
  ADD COLUMN next_attempt_at DATETIME(6) NULL,
  ADD COLUMN lease_owner VARCHAR(128) NULL,
  ADD COLUMN lease_until DATETIME(6) NULL,
  ADD COLUMN last_error TEXT NULL,
  ADD INDEX idx_dispatch_command (tenant_id, command_id),
  ADD UNIQUE KEY uk_dispatch_attempt (tenant_id, task_id, command_kind, attempt),
  ADD INDEX idx_dispatch_due (status, next_attempt_at);

ALTER TABLE task_dispatches
  MODIFY COLUMN status ENUM('pending', 'dispatched', 'acked', 'executing', 'succeeded', 'failed', 'timeout', 'cancelled', 'abandoned')
    NOT NULL DEFAULT 'pending',
  MODIFY COLUMN dispatched_at TIMESTAMP(6) NULL DEFAULT NULL;

ALTER TABLE task_status_history ADD COLUMN event_id VARCHAR(128) NULL;

-- Receipt dedup, late-result evidence and accepted status changes are different concerns.
-- Report ingestion and any task/audit/outbox updates must share one MySQL transaction.
CREATE TABLE task_execution_reports (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  task_id VARCHAR(128) NOT NULL,
  executor_id VARCHAR(128) NOT NULL,
  execution_key VARCHAR(128) NOT NULL,
  dispatch_id VARCHAR(256) NOT NULL,
  request_hash CHAR(64) NOT NULL,
  expected_status_version INT NOT NULL,
  reported_status VARCHAR(32) NOT NULL,
  result JSON NULL,
  reason TEXT NULL,
  occurred_at TIMESTAMP(6) NOT NULL,
  received_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  disposition ENUM('applied', 'late_result') NOT NULL,
  applied_status_version INT NULL,
  UNIQUE KEY uk_report_event (tenant_id, event_id),
  INDEX idx_report_task (tenant_id, task_id, received_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Execution feedback and late-result audit';

-- Do not use the generic consumer_dedup cleanup as an executor inbox TTL.
-- A simulator owns a local durable inbox/result transaction, implemented in P2.
