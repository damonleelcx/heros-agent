package adminops_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// rollout_test.go covers task 13.3/13.4: the rollout gate (8a behind a flag, dark until the checklist,
// 8b gated on its dependencies) and the expand-only, no-priced-value migration.

// TestRolloutIsDarkUntilEnabled: the safe default for the highest-blast-radius surface is that nothing
// serves.
func TestRolloutIsDarkUntilEnabled(t *testing.T) {
	r := adminops.NewRollout()
	if r.WaveEnabled(adminops.Wave8a) || r.WaveEnabled(adminops.Wave8b) {
		t.Fatal("a fresh rollout is not dark")
	}
	// 8a refuses until the checklist is green.
	if err := r.EnableWave8a(true); err == nil {
		t.Fatal("8a enabled before the M11-8a checklist was green")
	}
	r.MarkChecklistGreen()
	if err := r.EnableWave8a(true); err != nil {
		t.Fatalf("8a should enable once the checklist is green: %v", err)
	}
	if !r.WaveEnabled(adminops.Wave8a) {
		t.Fatal("8a did not come up after enabling")
	}
	if r.WaveEnabled(adminops.Wave8b) {
		t.Fatal("8b came up with 8a")
	}
}

// TestWave8bIsGatedOnItsDependencies: 8b cannot ship before the P6 kill switch and P2.5 aggregates.
func TestWave8bIsGatedOnItsDependencies(t *testing.T) {
	r := adminops.NewRollout()
	r.MarkChecklistGreen()
	if err := r.EnableWave8a(false); err != nil {
		t.Fatalf("EnableWave8a: %v", err)
	}
	if err := r.EnableWave8b(); err == nil {
		t.Fatal("8b enabled with neither dependency live")
	}
	r.MarkKillSwitchLive()
	if err := r.EnableWave8b(); err == nil {
		t.Fatal("8b enabled with the P2.5 aggregates still down")
	}
	r.MarkAggregatesLive()
	if err := r.EnableWave8b(); err != nil {
		t.Fatalf("8b should enable once both dependencies are live: %v", err)
	}
	if !r.WaveEnabled(adminops.Wave8b) {
		t.Fatal("8b did not come up")
	}
}

// TestEveryCapabilityHasARolloutWave: a capability with no wave would ship in neither, or silently in
// both. The map must cover the whole enumeration.
func TestEveryCapabilityHasARolloutWave(t *testing.T) {
	for _, c := range adminrbac.Capabilities {
		if _, ok := adminops.CapabilityWave[string(c)]; !ok {
			t.Errorf("capability %s has no rollout wave", c)
		}
	}
}

// TestRolloutDescribeCarriesNoSecret: the readiness surface reports words, never a secret.
func TestRolloutDescribeCarriesNoSecret(t *testing.T) {
	r := adminops.NewRollout()
	r.MarkChecklistGreen()
	_ = r.EnableWave8a(true)
	for k, v := range r.Describe() {
		if v != "on" && v != "off" {
			t.Errorf("rollout describe key %q has non-boolean value %q — it should carry only on/off words", k, v)
		}
	}
}

// TestP8MigrationIsExpandOnlyAndPriceless: task 13.3. The 0014 migration must ADD only, be idempotent,
// and carry no priced value. Runs without a database, on the SQL text.
func TestP8MigrationIsExpandOnlyAndPriceless(t *testing.T) {
	root := repoRoot(t)
	up := readFileT(t, filepath.Join(root, "db", "migrations", "postgres", "0014_p8_admin_console.up.sql"))

	// Expand-only: no destructive DDL against an existing object.
	for _, forbidden := range []string{"ALTER COLUMN", "DROP COLUMN", "DROP TABLE", "TRUNCATE"} {
		if strings.Contains(strings.ToUpper(up), forbidden) {
			t.Errorf("the up-migration contains %q — it must be expand-only", forbidden)
		}
	}
	// Idempotent: every table is IF NOT EXISTS, and the registry row is ON CONFLICT DO NOTHING.
	tableCreates := regexp.MustCompile(`(?i)CREATE TABLE `).FindAllString(up, -1)
	idempotentCreates := regexp.MustCompile(`(?i)CREATE TABLE IF NOT EXISTS`).FindAllString(up, -1)
	if len(tableCreates) != len(idempotentCreates) {
		t.Errorf("not every CREATE TABLE is IF NOT EXISTS: %d creates, %d guarded", len(tableCreates), len(idempotentCreates))
	}
	if len(idempotentCreates) != 8 {
		t.Errorf("the migration creates %d tables, want the 8 P8 tables", len(idempotentCreates))
	}
	if !strings.Contains(strings.ToUpper(up), "ON CONFLICT") {
		t.Error("the schema_migrations insert is not ON CONFLICT DO NOTHING — a re-run would error")
	}

	// The append-only tables carry a write-once trigger.
	for _, table := range []string{"audit_entry", "admin_role_grant"} {
		if !strings.Contains(up, "trg_"+table+"_append_only") {
			t.Errorf("the %s table has no append-only trigger — a mutate path would exist", table)
		}
	}

	// No priced value.
	if pricedValue.MatchString(up) {
		t.Error("the P8 migration contains a priced literal — plans and price refs live in the config store")
	}

	// The down-migration exists and drops exactly what the up creates.
	down := readFileT(t, filepath.Join(root, "db", "migrations", "postgres", "0014_p8_admin_console.down.sql"))
	for _, table := range []string{
		"admin_principal", "admin_role_grant", "admin_session", "permission",
		"audit_entry", "impersonation_session", "kill_switch_state", "gdpr_request",
	} {
		if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("the up-migration does not create %s", table)
		}
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("the down-migration does not drop %s", table)
		}
	}
}

// TestAdminPrincipalHasNoTenantColumn: admin identity is separate from tenant identity, enforced in
// the schema (FR1).
func TestAdminPrincipalHasNoTenantColumn(t *testing.T) {
	root := repoRoot(t)
	up := readFileT(t, filepath.Join(root, "db", "migrations", "postgres", "0014_p8_admin_console.up.sql"))
	// Isolate the admin_principal table definition.
	start := strings.Index(up, "CREATE TABLE IF NOT EXISTS admin_principal")
	if start < 0 {
		t.Fatal("admin_principal not found")
	}
	end := strings.Index(up[start:], ");")
	def := up[start : start+end]
	// Strip comment lines: the definition's prose legitimately says "not a tenant principal". What must
	// be absent is a COLUMN named for a tenant.
	var columns []string
	for _, line := range strings.Split(def, "\n") {
		code := line
		if i := strings.Index(code, "--"); i >= 0 {
			code = code[:i]
		}
		columns = append(columns, strings.ToLower(strings.TrimSpace(code)))
	}
	joined := strings.Join(columns, "\n")
	if strings.Contains(joined, "tenant_id") || strings.Contains(joined, "customer_id") || strings.Contains(joined, "tenant ") {
		t.Error("admin_principal has a tenant/customer column — an admin principal must never be a tenant principal")
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
