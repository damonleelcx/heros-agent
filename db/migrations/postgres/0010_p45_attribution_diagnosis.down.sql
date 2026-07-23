-- Down-migration for P4.5 attribution + diagnosis. Drops only the six report tables this migration
-- added; it touches nothing P0/P2/P4 created. Dropping is safe precisely because these tables have no
-- write path into any other store — nothing downstream depends on them for correctness of a config.

BEGIN;

DROP TABLE IF EXISTS analyst_cal;
DROP TABLE IF EXISTS diagnosis;
DROP TABLE IF EXISTS bottleneck_flag;
DROP TABLE IF EXISTS ablation_result;
DROP TABLE IF EXISTS failure_cluster;
DROP TABLE IF EXISTS attribution;

DELETE FROM schema_migrations WHERE id = 10;

COMMIT;
