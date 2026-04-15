package tooldef

import (
	"context"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"net"
	"net/url"
	"strconv"
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
		return toolcontract.Ok("url-safety", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "check", "validate":
		raw := strings.TrimSpace(asString(args["url"]))
		if raw == "" {
			return toolcontract.Error("url-safety", toolcontract.ErrorCodeValidationError, "missing url", action, args), nil
		}
		resolveDNS := asBool(args["resolve_dns"])
		safe, risk, reason, host, scheme, dns := evaluateURL(raw, resolveDNS)
		return toolcontract.Ok("url-safety", action, args, map[string]any{
			"url":       raw,
			"host":      host,
			"scheme":    scheme,
			"allow":     safe,
			"safe":      safe,
			"risk":      risk,
			"reason":    reason,
			"signals":   buildSignals(raw, resolveDNS, dns),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("url-safety", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func evaluateURL(raw string, resolveDNS bool) (bool, string, string, string, string, map[string]any) {
	dnsInfo := map[string]any{"resolved": false, "private_ip_found": false, "ips": []string{}}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false, "high", "invalid URL", "", "", dnsInfo
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if u.User != nil {
		return false, "high", "userinfo in URL is blocked", host, scheme, dnsInfo
	}
	if scheme != "http" && scheme != "https" {
		return false, "high", "only http/https are allowed", host, scheme, dnsInfo
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false, "high", "local/internal hostnames are blocked", host, scheme, dnsInfo
	}
	port := u.Port()
	if port != "" {
		p, _ := strconv.Atoi(port)
		if p <= 0 || p > 65535 {
			return false, "high", "invalid port", host, scheme, dnsInfo
		}
		if p == 22 || p == 2375 || p == 3306 || p == 5432 || p == 6379 {
			return false, "high", "sensitive service port is blocked", host, scheme, dnsInfo
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return false, "high", "private or local network address is blocked", host, scheme, dnsInfo
		}
	}
	if resolveDNS && net.ParseIP(host) == nil {
		if blocked, info := dnsResolutionBlocked(host); blocked {
			return false, "high", "dns resolution points to private/local address", host, scheme, info
		} else {
			dnsInfo = info
		}
	}
	if strings.Contains(u.Path, "..") {
		return false, "medium", "suspicious path traversal pattern", host, scheme, dnsInfo
	}
	return true, "low", "URL passed safety checks", host, scheme, dnsInfo
}

func buildSignals(raw string, resolveDNS bool, dns map[string]any) map[string]any {
	u, err := url.Parse(raw)
	if err != nil {
		return map[string]any{"valid_url": false}
	}
	host := strings.ToLower(u.Hostname())
	out := map[string]any{
		"valid_url":        true,
		"has_userinfo":     u.User != nil,
		"has_query":        u.RawQuery != "",
		"local_hostname":   host == "localhost" || strings.HasSuffix(host, ".local"),
		"path_traversal":   strings.Contains(u.Path, ".."),
		"explicit_port":    u.Port() != "",
		"dns_resolution_enabled": resolveDNS,
	}
	if dns != nil {
		for k, v := range dns {
			out["dns_"+k] = v
		}
	}
	return out
}

func dnsResolutionBlocked(host string) (bool, map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	info := map[string]any{"resolved": false, "private_ip_found": false, "ips": []string{}}
	if err != nil || len(addrs) == 0 {
		info["error"] = "dns_lookup_failed_or_empty"
		return false, info
	}
	info["resolved"] = true
	ips := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ip := a.IP
		ips = append(ips, ip.String())
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			info["private_ip_found"] = true
		}
	}
	info["ips"] = ips
	return info["private_ip_found"].(bool), info
}

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
