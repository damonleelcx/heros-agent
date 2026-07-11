package providergateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChatCompletion_ParsesContentAndToolCalls verifies the OpenAI-compatible
// transport parses a non-streaming response into content + tool calls and sends
// a well-formed request (model, auth header, path).
func TestChatCompletion_ParsesContentAndToolCalls(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hello","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]}}]}`)
	}))
	defer srv.Close()

	content, calls, err := ChatCompletion(context.Background(), srv.URL, "test-key", "gpt-x",
		[]map[string]any{{"role": "user", "content": "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if content != "hello" {
		t.Errorf("content = %q, want %q", content, "hello")
	}
	if len(calls) != 1 || calls[0].Name != "lookup" || calls[0].ID != "c1" || calls[0].Arguments != `{"q":1}` {
		t.Errorf("tool calls = %+v, want one lookup call", calls)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotBody["model"] != "gpt-x" {
		t.Errorf("request model = %v, want gpt-x", gotBody["model"])
	}
}

// TestChatCompletion_HTTPErrorSurfacesBody ensures non-2xx responses become errors.
func TestChatCompletion_HTTPErrorSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad model"}`)
	}))
	defer srv.Close()

	_, _, err := ChatCompletion(context.Background(), srv.URL, "k", "m", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error on 400 response, got nil")
	}
}
