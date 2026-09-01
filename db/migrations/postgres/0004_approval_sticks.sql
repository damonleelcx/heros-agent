-- 0004_approval_sticks — record that a person answered.
--
-- # The bug
--
-- The approval gate ran on every claim. A task parked awaiting approval, a person approved it, the
-- worker claimed it, the policy said "this still changes something outside the platform" — and it parked
-- again. Forever. The run never progressed, and every re-approval reported success because the task was
-- genuinely awaiting approval each time somebody looked.
--
-- The policy answers "does this KIND of work need a person?", which is a property of the task and does
-- not change. Whether a person has ALREADY ANSWERED is a different fact, and it was nowhere.

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS approved BOOLEAN NOT NULL DEFAULT FALSE;
