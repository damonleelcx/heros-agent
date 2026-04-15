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
		return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if action == "status" {
		return toolcontract.Ok("homeassistant-tool", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	}
	requestURL := strings.TrimSpace(asString(args["url"]))
	baseURL := strings.TrimSpace(asString(args["base_url"]))
	path := strings.TrimSpace(asString(args["path"]))
	if requestURL == "" {
		if baseURL == "" || path == "" {
			return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeValidationError, "missing url or (base_url + path)", action, args), nil
		}
		requestURL = strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
	}
	if err := validateURL(requestURL); err != nil {
		return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	method := strings.ToUpper(strings.TrimSpace(asString(args["method"])))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
		return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeValidationError, "unsupported method", action, args), nil
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
		return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	token := strings.TrimSpace(asString(args["token"]))
	if token == "" {
		token = strings.TrimSpace(asString(args["access_token"]))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return toolcontract.Error("homeassistant-tool", toolcontract.ErrorCodeNetworkError, err.Error(), action, args), nil
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return toolcontract.Ok("homeassistant-tool", action, args, map[string]any{
		"url":         requestURL,
		"method":      method,
		"status_code": resp.StatusCode,
		"body":        string(rb),
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}), nil
}

func asString(v any) string { s, _ := v.(string); return s }

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errInvalid("invalid url")
	}
	s := strings.ToLower(u.Scheme)
	if s != "http" && s != "https" {
		return errInvalid("only http/https are allowed")
	}
	if u.User != nil {
		return errInvalid("userinfo is not allowed in url")
	}
	return nil
}

type invalidErr string

func (e invalidErr) Error() string { return string(e) }
func errInvalid(s string) error    { return invalidErr(s) }
