-- Reverses 0036. A review and recovery artifact: the deployed binary embeds only `.up.sql` files and
-- cannot run this (db/migrations/embed.go), because P19 Decision 7 makes rollback "re-apply the prior
-- package" rather than "let the process drop the customer's tables on some code path".
--
-- 🔴 Dropping admin_model_closed_price destroys NON-RETROACTIVITY: the price references closed periods
-- were billed under are not recoverable from anywhere else, so every closed period silently re-resolves
-- against today's references and last quarter's SUM changes.

BEGIN;

DROP TABLE IF EXISTS admin_model_closed_price;
DROP TABLE IF EXISTS admin_model;

DELETE FROM schema_migrations WHERE id = 36;

COMMIT;
