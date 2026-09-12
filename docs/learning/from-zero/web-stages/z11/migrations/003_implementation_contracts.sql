-- Apply only after the runner has rejected duplicate legacy audit versions.
ALTER TABLE tasks ADD COLUMN result JSON NULL, ADD COLUMN completed_at TIMESTAMP(6) NULL;
ALTER TABLE task_status_history ADD UNIQUE KEY uk_task_status_version (tenant_id, task_id, status_version);
ALTER TABLE task_dispatches
 ADD COLUMN dlq_at TIMESTAMP(6) NULL,
 ADD COLUMN dlq_published_at TIMESTAMP(6) NULL,
 ADD COLUMN last_task_status INT NOT NULL DEFAULT 0,
 ADD COLUMN last_task_status_version INT NOT NULL DEFAULT 0,
 ADD COLUMN retry_round INT NOT NULL DEFAULT 0,
 ADD COLUMN command_payload JSON NULL,
 MODIFY COLUMN status ENUM('pending','dispatched','acked','executing','succeeded','failed','timeout','cancelled','abandoned','dlq') NOT NULL DEFAULT 'pending';
CREATE TABLE history_sample_keys (
 tenant_id VARCHAR(64) NOT NULL,
 source_id VARCHAR(64) NOT NULL,
 source_generation BIGINT NOT NULL,
 event_id VARCHAR(128) NOT NULL,
 sampled_at DATETIME(6) NOT NULL,
 sample_id BIGINT NOT NULL,
 PRIMARY KEY (tenant_id, source_id, source_generation, event_id),
 INDEX idx_history_key_retention (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE course_bindings (
 binding_key VARCHAR(256) PRIMARY KEY,
 binding_json JSON NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
ALTER TABLE entity_history_samples MODIFY COLUMN occurred_at DATETIME(6) NOT NULL,
 ADD COLUMN source_generation BIGINT NOT NULL DEFAULT 1,
 ADD INDEX idx_history_cursor (tenant_id,entity_id,occurred_at,id);
