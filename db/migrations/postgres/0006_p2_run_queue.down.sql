-- Rollback for 0006_p2_run_queue.up.sql (task 6.3).
--
-- Dropping the queue discards PENDING work — items not yet dispatched are lost. That is safe only
-- because the queue is scaffolding: the durable records (transform, run, node_execution) are not
-- here, and an undispatched item can simply be re-enqueued from its spec.

BEGIN;

DROP TABLE IF EXISTS run_queue;

DELETE FROM schema_migrations WHERE id = 6;

COMMIT;
