// heros-mcp: stdio MCP server that bridges to a running agentd HTTP API (for IDE tool hosts).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	agentdURL = flag.String("agentd-url", "http://127.0.0.1:8787", "agentd base URL")
	apiKey    = flag.String("api-key", "", "X-API-Key when auth_mode=required")
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func main() {
	flag.Parse()
	sc := bufio.NewScanner(os.Stdin)
	// Large lines for tool payloads
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}
		switch req.Method {
		case "initialize":
			writeResult(req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "heros-sidecar-mcp", "version": "0.2.0"},
			})
		case "notifications/initialized":
			// no response for notifications
		case "tools/list":
			writeResult(req.ID, map[string]any{"tools": toolDefs()})
		case "tools/call":
			handleToolCall(req.ID, req.Params)
		default:
			writeErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "heros_health",
			"description": "GET /health — infra status (qdrant, neo4j, nats, sqlite).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "heros_memory_retrieve",
			"description": "Semantic / vector memory search against agentd.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query"},
					"k":     map[string]any{"type": "integer", "description": "Top-k", "default": 5},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "heros_submit_proposal",
			"description": "Queue a governance mutation (human approval) on the sidecar.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"layer":     map[string]any{"type": "string", "description": "prompt_engineering | context_engineering | harness_engineering | tooling"},
					"title":     map[string]any{"type": "string"},
					"rationale": map[string]any{"type": "string"},
					"diff":      map[string]any{"type": "string"},
				},
				"required": []string{"layer", "title", "rationale", "diff"},
			},
		},
	}
}

func handleToolCall(id json.RawMessage, params json.RawMessage) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	var status int
	var body []byte
	var err error
	switch p.Name {
	case "heros_health":
		status, body, err = doHTTP(http.MethodGet, "/health", nil)
	case "heros_memory_retrieve":
		var a struct {
			Query string `json:"query"`
			K     int    `json:"k"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		if a.K <= 0 {
			a.K = 5
		}
		b, _ := json.Marshal(map[string]any{"query": a.Query, "k": a.K})
		status, body, err = doHTTP(http.MethodPost, "/api/memory/retrieve", b)
	case "heros_submit_proposal":
		status, body, err = doHTTP(http.MethodPost, "/api/proposals", p.Arguments)
	default:
		writeErr(id, -32602, "unknown tool "+p.Name)
		return
	}
	if err != nil {
		writeErr(id, -32000, err.Error())
		return
	}
	out := fmt.Sprintf("HTTP %d\n%s", status, string(body))
	writeResult(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": out}},
	})
}

func doHTTP(method, path string, jsonBody []byte) (int, []byte, error) {
	u := strings.TrimRight(*agentdURL, "/") + path
	var rdr io.Reader
	if jsonBody != nil {
		rdr = bytes.NewReader(jsonBody)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return 0, nil, err
	}
	if jsonBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if *apiKey != "" {
		req.Header.Set("X-API-Key", *apiKey)
	}
	c := &http.Client{Timeout: 120 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func writeResult(id json.RawMessage, result any) {
	m := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}

func writeErr(id json.RawMessage, code int, message string) {
	m := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error":   map[string]any{"code": code, "message": message},
	}
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}
