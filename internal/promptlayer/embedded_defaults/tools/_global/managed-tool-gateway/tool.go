package tooldef

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "forward"
	}
	st, err := loadState(stateFile(asString(args["_workdir"])))
	if err != nil {
		return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	switch action {
	case "status":
		nonceCount := 0
		if nm, ok := st["nonces"].(map[string]any); ok {
			nonceCount = len(nm)
		}
		return toolcontract.Ok("managed-tool-gateway", action, args, map[string]any{
			"status":    "ready",
			"routes":    sortedKeys(st),
			"nonce_count": nonceCount,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "list_routes":
		keys := sortedKeys(st)
		return toolcontract.Ok("managed-tool-gateway", action, args, map[string]any{
			"routes":    keys,
			"count":     len(keys),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "register_route", "delete_route":
		if !policyBool(args, "allow_admin", false) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodePermissionDenied, "policy allow_admin=false", action, args), nil
		}
		route := strings.TrimSpace(asString(args["route"]))
		if route == "" {
			route = strings.TrimSpace(asString(args["target_tool"]))
		}
		if route == "" {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "missing route", action, args), nil
		}
		if !validID(route) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid route id", action, args), nil
		}
		if action == "register_route" {
			target := strings.TrimSpace(asString(args["target_tool"]))
			if target == "" {
				target = route
			}
			if !validID(target) {
				return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid target_tool id", action, args), nil
			}
			st[route] = map[string]any{
				"target_tool": target,
				"updated_at":  time.Now().UTC().Format(time.RFC3339),
			}
		} else {
			delete(st, route)
		}
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("managed-tool-gateway", action, args, map[string]any{
			"updated":   true,
			"route":     route,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "forward":
		route := strings.TrimSpace(asString(args["route"]))
		if route == "" {
			route = strings.TrimSpace(asString(args["target_tool"]))
		}
		if route == "" {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "missing route or target_tool", action, args), nil
		}
		if !validID(route) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid route id", action, args), nil
		}
		target := route
		if entry, ok := st[route].(map[string]any); ok {
			if t := strings.TrimSpace(asString(entry["target_tool"])); t != "" {
				target = t
			}
		}
		if !validID(target) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid target_tool id", action, args), nil
		}
		payload, _ := args["payload"].(map[string]any)
		if payload == nil {
			payload = map[string]any{}
		}
		if !policyBool(args, "allow_exec", true) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodePermissionDenied, "policy allow_exec=false", action, args), nil
		}
		if b, _ := json.Marshal(payload); len(b) > 64*1024 {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "payload too large (>64KB)", action, args), nil
		}
		nonce := strings.TrimSpace(asString(args["nonce"]))
		if nonce == "" {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "missing nonce", action, args), nil
		}
		if !validNonce(nonce) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid nonce format", action, args), nil
		}
		if seenNonce(st, nonce) {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodePolicyBlocked, "replayed nonce", action, args), nil
		}
		signingKey := signingKey(args)
		if signingKey != "" {
			sig := strings.TrimSpace(asString(args["signature"]))
			payloadBytes, _ := json.Marshal(payload)
			if sig == "" || !verifySignature(signingKey, route, target, nonce, payloadBytes, sig) {
				return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeValidationError, "invalid signature", action, args), nil
			}
		}
		recordNonce(st, nonce)
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("managed-tool-gateway", action, args, map[string]any{
			"forwarded":   true,
			"target_tool": target,
			"route":       route,
			"nonce":       nonce,
			"payload":     payload,
			"note":        "dispatch envelope created; execution handled by caller",
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("managed-tool-gateway", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func stateFile(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "state", "managed-tool-gateway.json")
}

func loadState(path string) (map[string]any, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	st := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st, nil
}

func saveState(path string, st map[string]any) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
var nonceRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

func validID(v string) bool {
	return idRe.MatchString(strings.ToLower(strings.TrimSpace(v)))
}

func validNonce(v string) bool { return nonceRe.MatchString(v) }

func signingKey(args map[string]any) string {
	k := strings.TrimSpace(asString(args["signing_key"]))
	if k != "" {
		return k
	}
	return strings.TrimSpace(os.Getenv("MANAGED_TOOL_GATEWAY_SIGNING_KEY"))
}

func verifySignature(key, route, target, nonce string, payload []byte, sigHex string) bool {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(route))
	mac.Write([]byte{0})
	mac.Write([]byte(target))
	mac.Write([]byte{0})
	mac.Write([]byte(nonce))
	mac.Write([]byte{0})
	mac.Write(payload)
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, got)
}

func seenNonce(st map[string]any, nonce string) bool {
	m, ok := st["nonces"].(map[string]any)
	if !ok || m == nil {
		return false
	}
	expRaw, ok := m[nonce]
	if !ok {
		return false
	}
	exp := parseInt64(expRaw)
	now := time.Now().UTC().Unix()
	if exp < now {
		delete(m, nonce)
		st["nonces"] = m
		return false
	}
	return true
}

func recordNonce(st map[string]any, nonce string) {
	m, ok := st["nonces"].(map[string]any)
	if !ok || m == nil {
		m = map[string]any{}
	}
	now := time.Now().UTC().Unix()
	ttl := int64(10 * 60) // 10 minutes replay window
	m[nonce] = now + ttl
	// prune expired / keep bounded
	if len(m) > 2048 {
		for k, v := range m {
			if parseInt64(v) < now {
				delete(m, k)
			}
		}
	}
	st["nonces"] = m
}

func parseInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return i
	default:
		return 0
	}
}
