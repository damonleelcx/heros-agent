package tooldef

import (
	"bytes"
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "query"
	}
	switch action {
	case "status":
		return toolcontract.Ok("osv-check", action, args, map[string]any{
			"status":    "ready",
			"endpoint":  "https://api.osv.dev/v1/query",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "query":
		ecosystem := strings.TrimSpace(asString(args["ecosystem"]))
		pkg := strings.TrimSpace(asString(args["package"]))
		version := strings.TrimSpace(asString(args["version"]))
		if ecosystem == "" || pkg == "" {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeValidationError, "missing ecosystem or package", action, args), nil
		}
		if !validName(ecosystem) || !validPkg(pkg) {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeValidationError, "invalid ecosystem or package format", action, args), nil
		}
		if version != "" && len(version) > 128 {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeValidationError, "version too long", action, args), nil
		}
		payload := map[string]any{
			"package": map[string]any{
				"ecosystem": ecosystem,
				"name":      pkg,
			},
		}
		if version != "" {
			payload["version"] = version
		}

		offline := map[string]any{
			"query":       payload,
			"fetched":     false,
			"vulns_count": 0,
			"note":        "set fetch=true to query live OSV API",
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}
		if !asBool(args["fetch"]) {
			return toolcontract.Ok("osv-check", action, args, offline), nil
		}
		if !policyBool(args, "allow_exec", true) {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodePermissionDenied, "policy allow_exec=false", action, args), nil
		}

		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, "https://api.osv.dev/v1/query", bytes.NewReader(body))
		if err != nil {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 12 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeNetworkError, err.Error(), action, args), nil
		}
		defer resp.Body.Close()

		var decoded map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return toolcontract.Error("osv-check", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		vulns := 0
		if arr, ok := decoded["vulns"].([]any); ok {
			vulns = len(arr)
		}
		return toolcontract.Ok("osv-check", action, args, map[string]any{
			"query":       payload,
			"fetched":     true,
			"status_code": resp.StatusCode,
			"vulns_count": vulns,
			"response":    decoded,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("osv-check", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

func policyBool(args map[string]any, key string, def bool) bool {
	p, _ := args["policy"].(map[string]any)
	if p == nil {
		return def
	}
	b, ok := p[key].(bool)
	if !ok {
		return def
	}
	return b
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._+\-]{1,64}$`)
var pkgRe = regexp.MustCompile(`^[A-Za-z0-9._/\-]{1,256}$`)

func validName(v string) bool { return nameRe.MatchString(v) }
func validPkg(v string) bool  { return pkgRe.MatchString(v) }
