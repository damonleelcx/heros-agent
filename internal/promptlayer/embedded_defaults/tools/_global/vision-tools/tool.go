package tooldef

import (
	"bytes"
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"io"
	"net/http"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "request"
	}
	if action != "request" {
		return toolcontract.Error("vision-tools", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	url := strings.TrimSpace(asString(args["url"]))
	if url == "" {
		return toolcontract.Error("vision-tools", toolcontract.ErrorCodeValidationError, "missing url", action, args), nil
	}
	method := strings.ToUpper(strings.TrimSpace(asString(args["method"])))
	if method == "" {
		method = "GET"
	}
	body := asString(args["body"])
	if body == "" {
		if p, ok := args["payload"].(map[string]any); ok {
			b, _ := json.Marshal(p)
			body = string(b)
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		return toolcontract.Error("vision-tools", toolcontract.ErrorCodeValidationError, err.Error(), action, args), nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return toolcontract.Error("vision-tools", toolcontract.ErrorCodeNetworkError, err.Error(), action, args), nil
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return toolcontract.Ok("vision-tools", action, args, map[string]any{"url": url, "status_code": resp.StatusCode, "body": string(rb)}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
