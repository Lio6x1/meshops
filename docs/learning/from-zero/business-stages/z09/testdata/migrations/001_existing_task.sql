-- CI-only upgrade fixture: insert before 002, never into a user's database.
INSERT INTO tasks (tenant_id, task_id, idempotency_key, task_type, target_entity_id, payload, created_by)
VALUES ('demo_tenant', 'legacy-task', 'legacy-request', 'inspect', 'drone-001', JSON_OBJECT('duration_seconds', 5), 'test-operator');

INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
VALUES ('task.created', 'task', 'legacy-task', JSON_OBJECT('taskId', 'legacy-task'));
