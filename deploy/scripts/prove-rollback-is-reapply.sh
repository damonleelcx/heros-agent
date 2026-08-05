#!/usr/bin/env bash
# prove-rollback-is-reapply.sh — deploy the PRIOR image against the NEW schema and see what happens.
#
# P27 task 10.1. "Rollback is re-apply" is the deployment contract this repository's migrations are
# written against: schema changes are additive, so backing an image out is a redeploy of the previous
# image and never a down-migration. Nothing checked it. `pgmigrate.Apply` iterates its OWN embedded set
# and ignores ledger rows it does not recognise, so a prior binary BOOTS against a newer schema — which
# is the half everybody tests, and the half that was never in doubt.
#
# The half that matters is whether it can READ. A column that was NOT NULL when the prior image was
# compiled, and is nullable now, is a `sql: Scan error ... converting NULL to string is unsupported` the
# moment the newer image writes one — not at boot, not on the upgrade, but on the first read after the
# rollback, in whichever code path happens to touch that row.
#
# # How it works
#
#   1. A throwaway Postgres (db/migrations/postgres/run_pg_docker.sh).
#   2. THIS tree applies every migration and writes the row shapes only the new code can produce.
#   3. A git worktree at the prior ref — the actual prior image's source, not a re-implementation of it.
#   4. A probe generated INTO that worktree reads those rows through the prior tree's own store types.
#
# Step 4 is the point. Reproducing the prior query in a test in this tree would be pinning a
# transcription; compiling it against the prior package is the only way the answer is about the code that
# is actually deployed.
#
# # USAGE
#
#   deploy/scripts/prove-rollback-is-reapply.sh [<prior-ref>]
#
# <prior-ref> defaults to HEAD, which is correct while a change is uncommitted: the prior image is the
# last commit. On a released tree, pass the tag that is deployed.
#
# Exit 0 = the prior image reads everything the new one wrote. Exit 1 = it cannot, and the output names
# the row and the column.
set -euo pipefail

PRIOR_REF="${1:-HEAD}"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PROBE_PKG="internal/rollbackprobe"

die() { echo "prove-rollback: FATAL: $*" >&2; exit 1; }

command -v git >/dev/null 2>&1 || die "git is required"
command -v go  >/dev/null 2>&1 || die "go is required"

WORKTREE="$(mktemp -d)/prior"
cleanup() {
  git -C "$REPO_ROOT" worktree remove "$WORKTREE" --force >/dev/null 2>&1 || true
  git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
  rm -rf "$REPO_ROOT/$PROBE_PKG"
}
trap cleanup EXIT

echo "== prior image: $PRIOR_REF =="
git -C "$REPO_ROOT" worktree add "$WORKTREE" "$PRIOR_REF" --detach >/dev/null 2>&1 ||
  die "cannot create a worktree at $PRIOR_REF"

# ── The NEW side: bring the schema up and write what only the new code writes. ─────────────────────────
#
# A free account is the row P27 introduced: a customer on a plan that charges nothing has no
# billing-provider customer, and minting one for everybody who tries the free tier is data we cannot
# un-send. This is the row that decides the answer, and it is why the first run of this script FAILED —
# absence was spelled NULL, which the prior reader cannot scan. It is `''` now, and this is the check
# that keeps it that way.
mkdir -p "$REPO_ROOT/$PROBE_PKG"
cat > "$REPO_ROOT/$PROBE_PKG/main.go" <<'GO'
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	_ "github.com/lib/pq"
)

func main() {
	db, err := sql.Open("postgres", os.Getenv("HEROS_TEST_POSTGRES_URL"))
	if err != nil {
		panic(err)
	}
	res, err := pgmigrate.Apply(context.Background(), db)
	if err != nil {
		panic(err)
	}
	fmt.Printf("new:  applied %d migrations\n", len(res.Applied))

	st, err := account.NewPGStore(db)
	if err != nil {
		panic(err)
	}
	// The row the prior image has no shape for.
	if _, err := st.Create(account.Account{
		CustomerID: "org_free", ActivePlanID: "free", PlanConfigVersion: "1",
		PlanCharges: false, CreatedAt: time.Now().UTC(),
	}); err != nil {
		panic(err)
	}
	fmt.Println("new:  wrote a free account (provider_customer_handle is absent, plan_charges = false)")
}
GO

# ── The PRIOR side: read them back through the prior tree's own types. ─────────────────────────────────
mkdir -p "$WORKTREE/$PROBE_PKG"
cat > "$WORKTREE/$PROBE_PKG/main.go" <<'GO'
package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	_ "github.com/lib/pq"
)

func main() {
	db, err := sql.Open("postgres", os.Getenv("HEROS_TEST_POSTGRES_URL"))
	if err != nil {
		panic(err)
	}
	st, err := account.NewPGStore(db)
	if err != nil {
		fmt.Println("prior: NewPGStore failed:", err)
		os.Exit(1)
	}
	failed := 0

	// A customer that predates the upgrade. This one MUST keep working — if it does not, the schema
	// change broke the rollback for everybody rather than only for the new row shapes.
	if _, err := st.Create(account.Account{
		CustomerID: "org_paying", ProviderCustomerHandle: "cus_ROLLBACKPROOF", ActivePlanID: "team",
		PlanConfigVersion: "1", CreatedAt: time.Now().UTC(),
	}); err != nil {
		fmt.Println("prior: could not write a pre-existing customer:", err)
		failed = 1
	}
	if _, err := st.Get("org_paying"); err != nil {
		fmt.Println("prior: FAIL Get(a pre-existing customer):", err)
		failed = 1
	} else {
		fmt.Println("prior: ok   Get(a pre-existing customer)")
	}

	// The row the new image wrote.
	if _, err := st.Get("org_free"); err != nil {
		fmt.Println("prior: FAIL Get(a free account the new image created):", err)
		failed = 1
	} else {
		fmt.Println("prior: ok   Get(a free account the new image created)")
	}

	// 🔴 And the blast radius. `List()` scans every row, so ONE unreadable account takes down every
	// caller of it — the operator console's tenant, delivery and cross-tenant views, adminlaunch, and
	// the billing webhook — for every customer, not only the one with the odd row.
	if all, err := st.List(); err != nil {
		fmt.Println("prior: FAIL List():", err)
		fmt.Println("prior:      -> every caller is down: adminops tenant/delivery/crosstenant, adminlaunch, billing webhook")
		failed = 1
	} else {
		fmt.Printf("prior: ok   List() returned %d accounts\n", len(all))
	}

	os.Exit(failed)
}
GO

run_both() {
  ( cd "$REPO_ROOT" && GOWORK=off go run "./$PROBE_PKG" )
  ( cd "$WORKTREE"  && GOWORK=off go run "./$PROBE_PKG" )
}
export -f run_both 2>/dev/null || true

SCRIPTLET="$(mktemp)"
cat > "$SCRIPTLET" <<EOF
set -e
cd "$REPO_ROOT" && GOWORK=off go run "./$PROBE_PKG"
cd "$WORKTREE"  && GOWORK=off go run "./$PROBE_PKG"
EOF

echo "== rolling forward, then back =="
if bash "$REPO_ROOT/db/migrations/postgres/run_pg_docker.sh" bash "$SCRIPTLET"; then
  echo
  echo "prove-rollback: PASS — the prior image reads every row the new one wrote."
  exit 0
fi

cat >&2 <<'EOF'

prove-rollback: FAILED — rollback is NOT re-apply for this schema change.

The prior image boots (pgmigrate ignores ledger rows it does not recognise) and then cannot read rows
the newer image created. That is worse than a failed boot: the rollback appears to succeed, and the
breakage arrives later, on whichever read path touches the row first.

An additive schema change is one the PRIOR reader can still SCAN, which is a stronger condition than
"every DDL statement is an ADD". Relaxing a NOT NULL passes the second and fails the first: the column's
Go type on the prior side has no representation for the absence, so the value is unreadable rather than
merely unexpected.
EOF
exit 1
