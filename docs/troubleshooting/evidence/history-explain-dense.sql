USE meshops_review_history_20260912;
UPDATE entity_history_samples SET occurred_at='2026-09-04' WHERE id BETWEEN 30001 AND 60000;
UPDATE entity_history_samples SET sampled_at='2026-09-04' WHERE id BETWEEN 60001 AND 90000;
ANALYZE TABLE entity_history_samples;
SELECT 'case-C-dense-backlog' AS experiment;
EXPLAIN ANALYZE SELECT id FROM entity_history_samples WHERE sampled_at<'2026-09-05' OR occurred_at<'2026-09-05' ORDER BY id LIMIT 500;
EXPLAIN ANALYZE SELECT id FROM entity_history_samples WHERE sampled_at<'2026-09-05' ORDER BY sampled_at,id LIMIT 500;
EXPLAIN ANALYZE SELECT id FROM entity_history_samples WHERE occurred_at<'2026-09-05' ORDER BY occurred_at,id LIMIT 500;