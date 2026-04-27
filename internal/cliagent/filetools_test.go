package cliagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mustArgs(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(b)
}

func TestDispatchFileToolsCRUD(t *testing.T) {
	tmp := t.TempDir()
	s := &Session{WorkDir: tmp}
	ctx := context.Background()

	// Create file with parent dirs.
	writeRes, err := s.DispatchTool(ctx, ToolCall{
		Name:      "heros_write_file",
		Arguments: mustArgs(t, map[string]any{"path": "src/tests/canvas.test.ts", "content": "export const ok = true;\n"}),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(writeRes, "canvas.test.ts") {
		t.Fatalf("unexpected write result: %s", writeRes)
	}

	// Read file back.
	readRes, err := s.DispatchTool(ctx, ToolCall{
		Name:      "heros_read_file",
		Arguments: mustArgs(t, map[string]any{"path": "src/tests/canvas.test.ts"}),
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var readPayload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(readRes), &readPayload); err != nil {
		t.Fatalf("unmarshal read result: %v", err)
	}
	if !strings.Contains(readPayload.Content, "export const ok = true;") {
		t.Fatalf("read content mismatch: %q", readPayload.Content)
	}

	// List files recursively and verify it shows the created file.
	listRes, err := s.DispatchTool(ctx, ToolCall{
		Name:      "heros_list_files",
		Arguments: mustArgs(t, map[string]any{"path": "src", "recursive": true}),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listRes, "canvas.test.ts") {
		t.Fatalf("list missing file: %s", listRes)
	}

	// Delete file.
	_, err = s.DispatchTool(ctx, ToolCall{
		Name:      "heros_delete_path",
		Arguments: mustArgs(t, map[string]any{"path": "src/tests/canvas.test.ts"}),
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Confirm deletion via read error.
	_, err = s.DispatchTool(ctx, ToolCall{
		Name:      "heros_read_file",
		Arguments: mustArgs(t, map[string]any{"path": "src/tests/canvas.test.ts"}),
	})
	if err == nil {
		t.Fatal("expected read error after delete")
	}
}

func TestDispatchFileToolsAbsolutePath(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "absolute.txt")
	s := &Session{WorkDir: filepath.Join(tmp, "workspace")}
	ctx := context.Background()
	_, err := s.DispatchTool(ctx, ToolCall{
		Name:      "heros_write_file",
		Arguments: mustArgs(t, map[string]any{"path": target, "content": "hello"}),
	})
	if err != nil {
		t.Fatalf("write absolute: %v", err)
	}
	r, err := s.DispatchTool(ctx, ToolCall{
		Name:      "heros_read_file",
		Arguments: mustArgs(t, map[string]any{"path": target}),
	})
	if err != nil {
		t.Fatalf("read absolute: %v", err)
	}
	if !strings.Contains(r, "hello") {
		t.Fatalf("unexpected read absolute result: %s", r)
	}
}

func TestDispatchEditGlobGrepAndWriteTodos(t *testing.T) {
	tmp := t.TempDir()
	s := &Session{WorkDir: tmp}
	ctx := context.Background()

	_, err := s.DispatchTool(ctx, ToolCall{
		Name:      "write_file",
		Arguments: mustArgs(t, map[string]any{"path": "src/app.txt", "content": "alpha\nbeta\n"}),
	})
	if err != nil {
		t.Fatalf("write_file alias: %v", err)
	}
	_, err = s.DispatchTool(ctx, ToolCall{
		Name:      "edit_file",
		Arguments: mustArgs(t, map[string]any{"path": "src/app.txt", "old_string": "beta", "new_string": "gamma"}),
	})
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	grepRes, err := s.DispatchTool(ctx, ToolCall{
		Name:      "grep",
		Arguments: mustArgs(t, map[string]any{"path": "src", "query": "gamma", "recursive": true}),
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(grepRes, "gamma") {
		t.Fatalf("grep expected gamma, got: %s", grepRes)
	}
	globRes, err := s.DispatchTool(ctx, ToolCall{
		Name:      "glob",
		Arguments: mustArgs(t, map[string]any{"path": ".", "pattern": "src/*.txt"}),
	})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(globRes, "app.txt") {
		t.Fatalf("glob expected app.txt, got: %s", globRes)
	}
	todoRes, err := s.DispatchTool(ctx, ToolCall{
		Name: "write_todos",
		Arguments: mustArgs(t, map[string]any{
			"todos": []map[string]any{
				{"content": "inspect workspace", "status": "completed"},
				{"content": "run tests", "status": "pending"},
			},
		}),
	})
	if err != nil {
		t.Fatalf("write_todos: %v", err)
	}
	if !strings.Contains(todoRes, ".heros") {
		t.Fatalf("write_todos expected persisted path, got: %s", todoRes)
	}
}
