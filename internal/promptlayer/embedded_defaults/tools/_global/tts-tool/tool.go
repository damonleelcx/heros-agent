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
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if action == "status" {
		return toolcontract.Ok("tts-tool", action, args, map[string]any{"status": "ready", "timestamp": time.Now().UTC().Format(time.RFC3339)}), nil
	}
	requestURL := strings.TrimSpace(asString(args["url"]))
	if requestURL == "" {
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeValidationError, "missing url", action, args), nil
	}
	if err := validateURL(requestURL); err != nil {
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	method := strings.ToUpper(strings.TrimSpace(asString(args["method"])))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeValidationError, "unsupported method", action, args), nil
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
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(asString(args["token"])); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return toolcontract.Error("tts-tool", toolcontract.ErrorCodeNetworkError, err.Error(), action, args), nil
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return toolcontract.Ok("tts-tool", action, args, map[string]any{"url": requestURL, "method": method, "status_code": resp.StatusCode, "body": string(rb), "timestamp": time.Now().UTC().Format(time.RFC3339)}), nil
}

func asString(v any) string { s, _ := v.(string); return s }

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return invalidErr("invalid url")
	}
	s := strings.ToLower(u.Scheme)
	if s != "http" && s != "https" {
		return invalidErr("only http/https are allowed")
	}
	if u.User != nil {
		return invalidErr("userinfo is not allowed in url")
	}
	return nil
}

type invalidErr string

func (e invalidErr) Error() string { return string(e) }
