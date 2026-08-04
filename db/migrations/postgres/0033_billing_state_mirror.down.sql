-- Down for 0033_billing_state_mirror. A review and recovery artifact — NOT something the deployed
-- binary can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This drops the mirrored provider state for every customer, and the platform cannot rebuild it. The
-- provider does not re-send a webhook that was already acknowledged, so a customer whose payment is
-- failing reverts to "payment fine, no card on file" and stays there until their payment fails AGAIN.
-- Nothing about that is visible in a log — from the process's point of view it simply starts with an
-- empty mirror, which is exactly the state it starts in on a fresh install.

BEGIN;

ALTER TABLE billing_state DROP CONSTRAINT IF EXISTS billing_state_card_display_needs_a_card;
ALTER TABLE billing_state DROP CONSTRAINT IF EXISTS billing_state_last4_is_four_digits;
DROP TABLE IF EXISTS billing_state;

DELETE FROM schema_migrations WHERE id = 33;

COMMIT;
