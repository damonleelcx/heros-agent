from pathlib import Path

ROOT = Path("C:/Users/damon/Downloads/heros-foreal/internal/promptlayer/embedded_defaults/tools/_global")

TEST_TMPL = """package tooldef

import "testing"

func TestExecuteContract(t *testing.T) {
\tres, err := Execute(map[string]any{
\t\t"action": "contract_check",
\t\t"_workdir": ".",
\t\t"_session_id": "contract-test",
\t\t"policy": map[string]any{
\t\t\t"allow_exec": true,
\t\t\t"allow_write": true,
\t\t\t"allow_admin": true,
\t\t\t"allow_sync": true,
\t\t\t"allow_dangerous": false,
\t\t},
\t})
\tif err != nil {
\t\tt.Fatalf("Execute error: %v", err)
\t}
\tif res == nil {
\t\tt.Fatal("response is nil")
\t}
\tif _, ok := res["tool_id"].(string); !ok {
\t\tt.Fatalf("missing tool_id string: %#v", res["tool_id"])
\t}
\tstatus, ok := res["status"].(string)
\tif !ok || (status != "ok" && status != "error") {
\t\tt.Fatalf("invalid status: %#v", res["status"])
\t}
\taction, ok := res["action"].(string)
\tif !ok || action == "" {
\t\tt.Fatalf("missing action: %#v", res["action"])
\t}
\taudit, ok := res["audit"].(map[string]any)
\tif !ok {
\t\tt.Fatalf("missing audit object: %#v", res["audit"])
\t}
\tfor _, key := range []string{"timestamp", "session_id", "workdir", "action"} {
\t\tv, ok := audit[key].(string)
\t\tif !ok || v == "" {
\t\t\tt.Fatalf("invalid audit.%s: %#v", key, audit[key])
\t\t}
\t}
\tif status == "error" {
\t\tcode, ok := res["error_code"].(string)
\t\tif !ok {
\t\t\tt.Fatalf("missing error_code: %#v", res["error_code"])
\t\t}
\t\tallowed := map[string]bool{
\t\t\t"INVALID_ACTION": true,
\t\t\t"VALIDATION_ERROR": true,
\t\t\t"PERMISSION_DENIED": true,
\t\t\t"POLICY_BLOCKED": true,
\t\t\t"IO_ERROR": true,
\t\t\t"NOT_FOUND": true,
\t\t\t"COMMAND_FAILED": true,
\t\t\t"NETWORK_ERROR": true,
\t\t\t"UNKNOWN_ERROR": true,
\t\t}
\t\tif !allowed[code] {
\t\t\tt.Fatalf("error_code not whitelisted: %s", code)
\t\t}
\t}
}
"""

for d in sorted(p for p in ROOT.iterdir() if p.is_dir()):
    (d / "tool_contract_test.go").write_text(TEST_TMPL, encoding="utf-8")

print("generated contract tests for", len([p for p in ROOT.iterdir() if p.is_dir()]), "tools")
