package tooldef

import (
	"bytes"
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "request"
	}
	if action != "request" && action != "status" {
		return toolcontract.Error("openrouter-client", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if action == "status" {
		return toolcontract.Ok("openrouter-client", action, args, map[string]any{
			"status":    "ready",
			"base":      "https://openrouter.ai",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	}
	requestURL := strings.TrimSpace(asString(args["url"]))
	if requestURL == "" {
		base := strings.TrimSpace(asString(args["base_url"]))
		if base == "" {
			base = "https://openrouter.ai"
		}
		path := strings.TrimSpace(asString(args["path"]))
		if path == "" {
			path = "/api/v1/models"
		}
		requestURL = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	}
	if err := validateOpenRouterURL(requestURL); err != nil {
		return toolcontract.Error("openrouter-client", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	method := strings.ToUpper(strings.TrimSpace(asString(args["method"])))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return toolcontract.Error("openrouter-client", toolcontract.ErrorCodeValidationError, "unsupported method", action, args), nil
	}
	body := asString(args["body"])
	if body == "" {
		if p, ok := args["payload"].(map[string]any); ok {
			b, _ := json.Marshal(p)
			body = string(b)
		}
	}
	req, err := http.NewRequest(method, requestURL, bytes.NewBufferString(body))
	if err != nil {
		return toolcontract.Error("openrouter-client", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	apiKey := strings.TrimSpace(asString(args["api_key"]))
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return toolcontract.Error("openrouter-client", toolcontract.ErrorCodeNetworkError, err.Error(), action, args), nil
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return toolcontract.Ok("openrouter-client", action, args, map[string]any{
		"url":         requestURL,
		"method":      method,
		"status_code": resp.StatusCode,
		"body":        string(rb),
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}), nil
}

func asString(v any) string { s, _ := v.(string); return s }

func validateOpenRouterURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return invalidErr("invalid url")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return invalidErr("only https is allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host != "openrouter.ai" && host != "api.openrouter.ai" {
		return invalidErr("host must be openrouter.ai or api.openrouter.ai")
	}
	if u.User != nil {
		return invalidErr("userinfo is not allowed in url")
	}
	return nil
}

type invalidErr string

func (e invalidErr) Error() string { return string(e) }
