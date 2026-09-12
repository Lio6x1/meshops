-- Preserve previously journaled migration checksums; add retention indexes.
ALTER TABLE history_sample_keys ADD INDEX idx_history_key_sample (sample_id);
ALTER TABLE entity_history_samples ADD INDEX idx_history_occurred_retention (occurred_at, id);
