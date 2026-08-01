// Package migrations embeds the platform's Postgres schema into the binary.
//
// It exists so the deployed process carries its own schema. Before this, the only Go code that applied
// db/migrations/postgres was cmd/demo/configui, which read the files from a RELATIVE path — so the
// migrations applied when someone ran a demo from the repository root, and on no deployment ever. A
// container has no repository root, an air-gapped operator has no checkout, and "upgrade preserves user
// state" (P19 Decision 7) needs a mechanism, not a demo.
//
// The embed lives beside the SQL rather than inside internal/pgmigrate because go:embed cannot reach a
// parent directory: a package under internal/ could not see these files at all without copying them,
// and a copied schema is two schemas that drift.
package migrations

import "embed"

// Postgres holds the platform's PostgreSQL migrations, `postgres/NNNN_name.up.sql`.
//
// Only the `.up.sql` files are embedded. The `.down.sql` files are a review and recovery artifact, not
// something the deployed process may run: P19 Decision 7 makes rollback "re-apply the prior package",
// and a binary that can drop the customer's tables on some code path is a binary that eventually does.
//
//go:embed postgres/*.up.sql
var Postgres embed.FS
