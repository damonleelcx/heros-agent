package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"net"
	"net/url"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "check"
	}
	switch action {
	case "status":
		return toolcontract.Ok("website-policy", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "check", "run", "evaluate":
		raw := strings.TrimSpace(asString(args["url"]))
		if raw == "" {
			return toolcontract.Error("website-policy", toolcontract.ErrorCodeValidationError, "missing url", action, args), nil
		}
		policy := evaluatePolicy(raw, asStringSlice(args["blocklist_hosts"]), asStringSlice(args["allowlist_hosts"]))
		return toolcontract.Ok("website-policy", action, args, map[string]any{
			"url":       raw,
			"host":      policy.host,
			"allow":     policy.allow,
			"risk":      policy.risk,
			"reason":    policy.reason,
			"signals":   policy.signals,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("website-policy", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

type websitePolicyResult struct {
	host    string
	allow   bool
	risk    string
	reason  string
	signals map[string]any
}

func evaluatePolicy(raw string, blocklist, allowlist []string) websitePolicyResult {
	blocklist = normalizeDomainRules(blocklist)
	allowlist = normalizeDomainRules(allowlist)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return websitePolicyResult{allow: false, risk: "high", reason: "invalid URL", signals: map[string]any{"valid_url": false}}
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.Path)
	signals := map[string]any{
		"valid_url":          true,
		"scheme":             scheme,
		"host_on_blocklist":  hostInList(host, blocklist),
		"host_on_allowlist":  hostInList(host, allowlist),
		"robots_path_signal": strings.Contains(path, "/robots.txt"),
		"admin_path_signal":  strings.Contains(path, "/admin"),
		"contains_query":     u.RawQuery != "",
		"has_userinfo":       u.User != nil,
	}
	if ip := net.ParseIP(host); ip != nil {
		signals["host_is_ip"] = true
		signals["private_ip"] = ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast()
	} else {
		signals["host_is_ip"] = false
		signals["private_ip"] = false
	}

	if scheme != "http" && scheme != "https" {
		return websitePolicyResult{host: host, allow: false, risk: "high", reason: "unsupported URL scheme", signals: signals}
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") || signals["private_ip"].(bool) {
		return websitePolicyResult{host: host, allow: false, risk: "high", reason: "local/private destination blocked", signals: signals}
	}
	if signals["has_userinfo"].(bool) {
		return websitePolicyResult{host: host, allow: false, risk: "high", reason: "userinfo in URL blocked", signals: signals}
	}
	if signals["host_on_blocklist"].(bool) {
		return websitePolicyResult{host: host, allow: false, risk: "high", reason: "host is blocklisted", signals: signals}
	}
	if len(allowlist) > 0 && !signals["host_on_allowlist"].(bool) {
		return websitePolicyResult{host: host, allow: false, risk: "medium", reason: "host not in allowlist", signals: signals}
	}
	if signals["admin_path_signal"].(bool) {
		return websitePolicyResult{host: host, allow: true, risk: "medium", reason: "admin endpoint detected; proceed cautiously", signals: signals}
	}
	if strings.Contains(path, "..") {
		return websitePolicyResult{host: host, allow: false, risk: "medium", reason: "suspicious traversal-like path", signals: signals}
	}
	return websitePolicyResult{host: host, allow: true, risk: "low", reason: "website policy checks passed", signals: signals}
}

func hostInList(host string, entries []string) bool {
	host = normalizeHost(host)
	for _, entry := range entries {
		e := normalizeRule(entry)
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "*.") {
			base := strings.TrimPrefix(e, "*.")
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
			continue
		}
		if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, _ := item.(string)
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeDomainRules(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, r := range in {
		n := normalizeRule(r)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func normalizeRule(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimPrefix(s, ".")
	if strings.HasPrefix(s, "*.") {
		base := strings.TrimPrefix(s, "*.")
		base = strings.Trim(base, ".")
		if base == "" {
			return ""
		}
		return "*." + base
	}
	return strings.Trim(s, ".")
}

func normalizeHost(h string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(h)), ".")
}
