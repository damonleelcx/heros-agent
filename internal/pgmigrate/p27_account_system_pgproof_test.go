//go:build pgproof

// Live-Postgres proof of migration 0038 — P27's account system.
//
// # What the existing proofs already cover, and what they cannot
//
// `TestASecondApplyIsANoOp` proves the RUNNER is idempotent: it reads the ledger and applies only what
// is missing. That is a property of `pgmigrate`, not of the SQL — the second apply never executes a
// byte of 0038, so a file full of bare `CREATE TABLE` would pass it exactly as well as this one does.
//
// The failure that gap leaves open is not hypothetical in this repository. A ledger row can be present
// without its DDL (a hand-patched database, a restore from a dump taken mid-migration) and it can be
// absent with the DDL present. In the second case the runner re-executes the file, and a file whose DDL
// is not itself idempotent turns a recoverable state into a platform that will not boot.
//
// So `TestMigration0038IsIdempotentWhenActuallyRerun` deletes the ledger row and lets the runner execute
// 0038 again, then compares a full structural fingerprint of the schema before and after. That is the
// assertion task 2.4 asks for, and the ledger cannot give it.
//
// # The three invariants that live in the database rather than in Go
//
//   - A paid plan may not hold a NULL provider handle. This is the whole of D3: the original NOT NULL
//     was correct for a BILLABLE account and made a free one inexpressible. Relaxing it without the
//     replacement CHECK would delete the guarantee rather than restate it.
//
//   - The card-data CHECK is untouched and still refuses a Luhn-valid PAN.
//
//   - The identity domain's foreign keys are real; the data plane's ownership columns deliberately have
//     none.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/pgmigrate/
package pgmigrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// schemaFingerprint is a stable, ordered description of everything 0038 creates or alters: columns with
// their types and nullability, constraints with their definitions, and indexes with theirs.
//
// It is deliberately NOT `SELECT count(*)`. A count is satisfied by the right number of wrong things,
// and the failure this guards against — a re-run that drops a NOT NULL, widens a CHECK, or replaces an
// index with a differently-shaped one — changes definitions while leaving counts identical.
func schemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder

	const tables = `'tenant','platform_user','membership','invitation','api_credential','console_session','account','run','variant_spec','eval_run'`

	rows, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default,'')
		  FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name IN (`+tables+`)
		 ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("introspect columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tbl, col, typ, nullable, def string
		if err := rows.Scan(&tbl, &col, &typ, &nullable, &def); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		b.WriteString("col " + tbl + "." + col + " " + typ + " null=" + nullable + " def=" + def + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns: %v", err)
	}

	crows, err := db.QueryContext(ctx, `
		SELECT t.relname, c.conname, pg_get_constraintdef(c.oid)
		  FROM pg_constraint c
		  JOIN pg_class     t ON t.oid = c.conrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		 WHERE n.nspname = current_schema() AND t.relname IN (`+tables+`)
		 ORDER BY t.relname, c.conname`)
	if err != nil {
		t.Fatalf("introspect constraints: %v", err)
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var tbl, name, def string
		if err := crows.Scan(&tbl, &name, &def); err != nil {
			t.Fatalf("scan constraint: %v", err)
		}
		b.WriteString("con " + tbl + "." + name + " " + def + "\n")
	}
	if err := crows.Err(); err != nil {
		t.Fatalf("constraints: %v", err)
	}

	irows, err := db.QueryContext(ctx, `
		SELECT tablename, indexname, indexdef FROM pg_indexes
		 WHERE schemaname = current_schema() AND tablename IN (`+tables+`)
		 ORDER BY tablename, indexname`)
	if err != nil {
		t.Fatalf("introspect indexes: %v", err)
	}
	defer func() { _ = irows.Close() }()
	for irows.Next() {
		var tbl, name, def string
		if err := irows.Scan(&tbl, &name, &def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		// The schema name is embedded in indexdef and differs per proof package; strip it so the
		// fingerprint compares structure rather than which schema this run happened to get.
		b.WriteString("idx " + tbl + "." + name + " " + strings.ReplaceAll(def, currentSchema(t, db)+".", "") + "\n")
	}
	if err := irows.Err(); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return b.String()
}

func currentSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&s); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	return s
}

// TestMigration0038IsIdempotentWhenActuallyRerun executes the file a second time, which the ledger
// normally prevents. See the package header for why the ledger's no-op is not this assertion.
func TestMigration0038IsIdempotentWhenActuallyRerun(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_rerun")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	before := schemaFingerprint(t, db)

	// Drop the ledger row so the runner considers 0038 unapplied and executes its SQL again. This is
	// the state a restore-from-a-mid-migration-dump leaves behind.
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = 38`); err != nil {
		t.Fatalf("clear ledger row: %v", err)
	}
	res, err := Apply(ctx, db)
	if err != nil {
		t.Fatalf("0038 is not idempotent: re-running it against a schema that already holds it failed.\n"+
			"A database whose ledger row was lost would now refuse to boot: %v", err)
	}
	if len(res.Applied) != 1 || !strings.Contains(res.Applied[0], "p27_account_system") {
		t.Fatalf("expected exactly 0038 to re-run, got %v", res.Applied)
	}

	if after := schemaFingerprint(t, db); after != before {
		t.Fatalf("re-running 0038 CHANGED the schema.\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestAccountHandleMayBeAbsentOnlyWhileThePlanChargesNothing is D3, checked in the database.
//
// The point of the original constraint was that a customer who cannot be billed must not look billable.
// Relaxing the column without this one would have deleted that guarantee instead of restating it, and
// "paid plan with no billing customer" would become a state something has to detect rather than a row
// that cannot exist.
//
// 🔴 ABSENT is the EMPTY STRING, not NULL — D3 as amended by task 10.1. The column keeps its NOT NULL and
// 0013's `<> ”` check is what was dropped, because the PRIOR image scans this column into a Go `string`
// and a NULL made every `account.List()` fail after a rollback. The invariant is identical; only its
// spelling moved, and the spelling is the one both images can read.
func TestAccountHandleMayBeAbsentOnlyWhileThePlanChargesNothing(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_handle")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A Free account with no provider handle is exactly what sign-up creates.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version, plan_charges)
		VALUES ('cus_free', '', 'free', 'cfg-1', FALSE)`); err != nil {
		t.Fatalf("a Free account with no provider handle must be storable — sign-up creates one: %v", err)
	}

	// The same row on a charging plan must be refused.
	_, err := db.ExecContext(ctx, `
		INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version, plan_charges)
		VALUES ('cus_paid_nohandle', '', 'team', 'cfg-1', TRUE)`)
	if err == nil {
		t.Fatal("the database accepted a CHARGING plan with no provider handle — the invariant the " +
			"original constraint carried has been deleted rather than restated")
	}
	if !strings.Contains(err.Error(), "account_handle_required_when_plan_charges") {
		t.Fatalf("refused, but by the wrong constraint: %v", err)
	}

	// 🔴 A NULL is not merely discouraged, it is unwritable — which is what makes the prior image's plain
	// `string` scan safe forever rather than safe until somebody writes one by hand.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version, plan_charges)
		VALUES ('cus_null', NULL, 'free', 'cfg-1', FALSE)`); err == nil {
		t.Fatal("the database accepted a NULL provider handle. The PRIOR image reads this column into a " +
			"Go `string`, and `List()` scans every row — one NULL takes down the operator console and the " +
			"billing webhook for every customer the moment anybody rolls back (task 10.1)")
	}

	// And the upgrade path: moving a free account onto a charging plan without minting a handle first
	// is the ordering mistake this constraint exists to catch.
	_, err = db.ExecContext(ctx, `UPDATE account SET active_plan_id='team', plan_charges=TRUE WHERE customer_id='cus_free'`)
	if err == nil {
		t.Fatal("an account was moved onto a charging plan while still holding no provider handle")
	}
}

// TestTheCardDataCheckIsUntouched: 0038 changes how ABSENCE is spelled and nothing else about this
// column. `”` passes the card-data check, as it must — there is nothing there to be a card number.
func TestTheCardDataCheckIsUntouched(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_pan")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 4242424242424242 is Luhn-valid and 16 digits — the shape of every PAN in circulation.
	for _, handle := range []string{"4242424242424242", "4242 4242 4242 4242", "4242-4242-4242-4242"} {
		_, err := db.ExecContext(ctx, `
			INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
			VALUES ($1, $2, 'team', 'cfg-1')`, "cus_pan_"+strings.NewReplacer(" ", "_", "-", "_").Replace(handle), handle)
		if err == nil {
			t.Fatalf("the account table accepted %q as a provider handle — that is card data, and this "+
				"platform has never been in PCI scope", handle)
		}
		if !strings.Contains(err.Error(), "account_handle_is_not_card_data") {
			t.Fatalf("refused %q by the wrong constraint: %v", handle, err)
		}
	}

	// ⚠️ The DATABASE is stricter than `account.NewHandle`, and this asserts the database.
	//
	// Go requires digits AND a valid Luhn checksum before refusing, and its comment claims "a legitimate
	// all-digit provider id is not rejected by accident". 0013's CHECK has no Luhn step — it is a plain
	// `!~ '^[0-9]{12,19}$'` — so it refuses every 12-19 digit handle, Luhn-valid or not. The database is
	// the last line, so the effective contract is the wider refusal, and this test pins THAT rather than
	// the comment. Found while proving 0038; 0013's constraint is deliberately not narrowed, because
	// tightening a CHECK on a deployed table is a one-way door with no P27 reason behind it.
	_, err := db.ExecContext(ctx, `
		INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		VALUES ('cus_numeric', '4242424242424241', 'team', 'cfg-1')`)
	if err == nil {
		t.Fatal("0013's card-data CHECK accepted a 16-digit handle. If that is intentional, the effective " +
			"contract has changed and account.NewHandle's Luhn step is now the only gate")
	}
	if !strings.Contains(err.Error(), "account_handle_is_not_card_data") {
		t.Fatalf("refused by the wrong constraint: %v", err)
	}

	// A provider handle that is not a bare digit run is storable, which is the shape every real billing
	// provider actually issues.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		VALUES ('cus_real', 'cus_9f3a21QpLm', 'team', 'cfg-1')`); err != nil {
		t.Fatalf("a normal provider handle was refused: %v", err)
	}
}

// TestOwnershipColumnsAreNullableAndPartiallyIndexed is D6 plus the index shape the design chose over
// CREATE INDEX CONCURRENTLY, which this runner cannot execute.
func TestOwnershipColumnsAreNullableAndPartiallyIndexed(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_owner")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 🔴 `proposal` is deliberately absent. Migration 0025 gave it `tenant_id NOT NULL` when P5.5's
	// console work landed, so proposals have been tenant-scoped for phases and have no pre-ownership
	// state. An earlier draft of 0038 added the column to it anyway; this fence is what caught it.
	for _, table := range []string{"run", "variant_spec", "eval_run"} {
		var nullable string
		err := db.QueryRowContext(ctx, `
			SELECT is_nullable FROM information_schema.columns
			 WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'tenant_id'`,
			table).Scan(&nullable)
		if err != nil {
			t.Fatalf("%s.tenant_id is missing: %v", table, err)
		}
		if nullable != "YES" {
			t.Errorf("%s.tenant_id is NOT NULL — a pre-P27 row has no recoverable owner, and NULL is the "+
				"only honest value for it", table)
		}
	}

	for _, idx := range []string{"idx_run_tenant", "idx_variant_spec_tenant", "idx_eval_run_tenant"} {
		var def string
		err := db.QueryRowContext(ctx,
			`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, idx).Scan(&def)
		if err != nil {
			t.Fatalf("%s is missing: %v", idx, err)
		}
		if !strings.Contains(def, "WHERE (tenant_id IS NOT NULL)") {
			t.Errorf("%s is not partial: %s\n"+
				"A full index would carry every pre-ownership row, which no query ever looks for, and "+
				"would have to be built over the whole deployed table while holding a lock", idx, def)
		}
	}
}

// TestTheIdentityDomainHasRealForeignKeysAndTheDataPlaneHasNone is §4's split, checked rather than
// asserted. Both halves matter: an orphan membership is a bug with no legitimate reading, and a foreign
// key from `run` would put an identity lookup in the data plane's write path.
func TestTheIdentityDomainHasRealForeignKeysAndTheDataPlaneHasNone(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_fk")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Inside the identity domain: a membership for a tenant that does not exist must be impossible.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO platform_user (user_id, issuer, subject, email) VALUES ('u1','https://idp','sub-1','a@acme.com')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO membership (user_id, tenant_id, role) VALUES ('u1','no_such_tenant','owner')`)
	if err == nil {
		t.Fatal("a membership was created for a tenant that does not exist")
	}

	// Across the boundary: `run.tenant_id` must NOT be a foreign key.
	var n int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_constraint c
		  JOIN pg_class     t ON t.oid = c.conrelid
		  JOIN pg_namespace s ON s.oid = t.relnamespace
		 WHERE s.nspname = current_schema() AND t.relname = 'run' AND c.contype = 'f'
		   AND pg_get_constraintdef(c.oid) LIKE '%tenant%'`).Scan(&n); err != nil {
		t.Fatalf("introspect run FKs: %v", err)
	}
	if n != 0 {
		t.Error("run.tenant_id has a foreign key into the identity domain — that puts a control-plane " +
			"lookup in the data plane's write path, and makes the pre-ownership NULL a special case")
	}
}

// TestTheOperatorDomainStaysDisjoint: P8 FR1, re-checked because this is the migration that would most
// plausibly break it. An operator is not a user.
func TestTheOperatorDomainStaysDisjoint(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_operator")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name IN ('admin_principal','admin_session','admin_role_grant')
		   AND column_name IN ('tenant_id','user_id')`).Scan(&n); err != nil {
		t.Fatalf("introspect operator tables: %v", err)
	}
	if n != 0 {
		t.Error("an operator table grew a tenant_id or user_id column — an operator principal is " +
			"categorically not a tenant principal (P8 FR1), and this migration must not have changed that")
	}
}

// TestTheSeedCanReuseAnExistingTenantIdShape: the seed creates tenants whose ids come from
// `cfg.TenantCredentials`, which are arbitrary strings customers already hold in their credentials.
// A schema that constrained the id's shape would break every deployment whose ids do not match.
func TestTheSeedCanReuseAnExistingTenantIdShape(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_ids")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, id := range []string{"acme", "tenant-with-dashes", "Tenant_With_Case", "t"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO tenant (tenant_id, name) VALUES ($1, $2)`, id, "n/"+id); err != nil {
			t.Errorf("tenant id %q was refused; every existing deployment's configured ids must seed: %v", id, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant (tenant_id, name) VALUES ('', 'empty')`); err == nil {
		t.Error("an empty tenant id was accepted")
	}
}

// TestTheOwnershipColumnsAreAddedWithoutRewritingTheDeployedTables is P27 task 10.6.
//
// # There is no backfill, and that is the answer rather than an omission
//
// The task asks for a backfill that is "conditional, resumable and interruptible, with progress readable
// from outside the process". The strongest form of every one of those properties is a migration that
// performs no rewrite at all, and that is what 0038 does: three `ADD COLUMN … NULL`, three PARTIAL
// indexes that cover zero rows at creation, and not one `UPDATE`.
//
// It is not laziness dressed as rigour. Design D6 decided it: the owner of a pre-P27 row was never
// written and cannot be recovered, and inferring one from a neighbouring `delivery` or `run_link` row
// produces a CONFIDENT WRONG owner on a table that bills money — an error nobody can falsify afterwards.
// So the residue stays NULL, is rendered as its own state everywhere, and is counted by
// `executor.Store.PreOwnedCount`, which is the "progress readable from outside the process" half: the
// number that would go down if a backfill ever ran is already readable, before one is written.
//
// # What this test actually protects
//
// The next person to look at those NULLs will want to fill them. The moment that becomes an UPDATE over
// the ownership column on existing rows, this migration stops being a metadata change and becomes a full
// rewrite of a deployed table, holding an ACCESS EXCLUSIVE lock inside a single transaction the runner
// cannot resume — every migration file here executes as one batch, which is also why
// `CREATE INDEX CONCURRENTLY` is unavailable. On a customer's `run` table that is an outage, and it
// arrives during the upgrade that was supposed to be additive.
func TestTheOwnershipColumnsAreAddedWithoutRewritingTheDeployedTables(t *testing.T) {
	db := openSchema(t, "pgmigrate_p27_norewrite")
	ctx := context.Background()

	// The SQL first, because a source assertion catches the rewrite that a data assertion would only
	// catch if the fixture happened to have a row the UPDATE matched.
	all, err := Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var body string
	for _, m := range all {
		if m.ID == 38 {
			body = m.SQL
		}
	}
	if body == "" {
		t.Fatal("migration 38 is not in the embedded set")
	}
	// Comments discuss backfilling by name — the design's rejected alternative is written down in this
	// file's header — so the statement, not the word, is what is banned.
	for _, stmt := range strings.Split(body, ";") {
		code := stmt
		if i := strings.Index(code, "--"); i >= 0 {
			// Crude but sufficient: every line in this file is either all comment or has none, and a
			// false positive here fails loudly rather than passing quietly.
			var kept []string
			for _, line := range strings.Split(stmt, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "--") {
					kept = append(kept, line)
				}
			}
			code = strings.Join(kept, "\n")
		}
		upper := strings.ToUpper(code)
		if strings.Contains(upper, "UPDATE ") && strings.Contains(upper, "TENANT_ID") {
			t.Errorf("0038 contains a statement that writes tenant_id on an existing table:\n%s\n"+
				"Every file here runs as ONE transaction, so this is an unresumable full rewrite of a "+
				"deployed table under an ACCESS EXCLUSIVE lock. D6 says the owner is not recoverable; if "+
				"that changed, the backfill belongs in its own conditional, restartable job — not here.",
				strings.TrimSpace(code))
		}
	}

	// And live: rows that existed BEFORE the ownership columns keep a NULL owner across the migration.
	// The source check above cannot see a rewrite performed by a trigger or a rule; this can.
	applyThrough(t, db, all, 37)
	seedPreOwnershipRun(t, db, "run_before_p27")
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply the rest: %v", err)
	}

	var owner sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT tenant_id FROM run WHERE run_id = $1`, "run_before_p27").Scan(&owner); err != nil {
		t.Fatalf("read the pre-P27 run back: %v", err)
	}
	if owner.Valid {
		t.Errorf("a run created before the ownership columns came out of the migration owned by %q. "+
			"Nothing wrote that, which means something guessed it — and a guessed owner on billed usage "+
			"is a money error nobody can falsify later.", owner.String)
	}
}

// applyThrough executes every migration up to and including maxID, so a test can put rows in a table
// the way a deployed platform would have — BEFORE the migration under test runs. Applying everything and
// then inserting proves nothing about a rewrite: the row would postdate the column.
func applyThrough(t *testing.T, db *sql.DB, all []Migration, maxID int64) {
	t.Helper()
	for _, m := range all {
		if m.ID > maxID {
			return
		}
		if _, err := db.Exec(m.SQL); err != nil {
			t.Fatalf("applying %s: %v", m.Name, err)
		}
	}
}

// seedPreOwnershipRun writes one run naming only the columns that existed before P27.
func seedPreOwnershipRun(t *testing.T, db *sql.DB, runID string) {
	t.Helper()
	const hash = "eeee111122223333444455556666777788889999aaaabbbbccccddddeeeeffff"
	// The lineage chain a run hangs off. `run.config_hash` is a real foreign key all the way back to
	// `workflow`, so a fixture that skipped it would fail on a constraint rather than on the property
	// under test — and a test that fails for the wrong reason is one nobody re-reads.
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		  VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			[]any{"wf_pre_p27", "https://example.invalid/repo", "abc123", "go", "1.0.0"}},
		{`INSERT INTO variant (variant_id, workflow_id, label) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			[]any{"v_pre_p27", "wf_pre_p27", "pre-ownership"}},
		{`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		  VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			[]any{hash, "v_pre_p27", "wf_pre_p27", "1.0.0", `{}`}},
		{`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		  VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			[]any{hash, "abc123", `{}`}},
		// `verification_strength` is named explicitly: migration 0007 added it with a DEFAULT and then
		// DROPPED that default in the same file, so a fixture omitting it hits a NOT NULL nobody expects.
		{`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		  VALUES ($1,$2,'built','type-checked') ON CONFLICT DO NOTHING`,
			[]any{hash, "abc123"}},
		{`INSERT INTO run (run_id, config_hash, source_revision, seed, status)
		  VALUES ($1,$2,$3,$4,'running')`,
			[]any{runID, hash, "abc123", 0}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed a pre-ownership run: %v", err)
		}
	}
}
