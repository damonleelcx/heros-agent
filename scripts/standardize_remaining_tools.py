from pathlib import Path

ROOT = Path("C:/Users/damon/Downloads/heros-foreal/internal/promptlayer/embedded_defaults/tools/_global")

MISSING = {
    "ansi-strip","approval","binary-extensions","browser-camofox","browser-camofox-state",
    "browser-providers-base","browser-providers-browser-use","browser-providers-browserbase","browser-providers-firecrawl",
    "browser-tool","budget-config","checkpoint-manager","clarify-tool","code-execution-tool","credential-files",
    "cronjob-tools","debug-helpers","delegate-tool","echo-safe","env-passthrough","evolution-reminder","file-operations",
    "file-tools","fuzzy-match","homeassistant-tool","image-generation-tool","interrupt","managed-tool-gateway","mcp-oauth",
    "mcp-tool","memory-tool","mixture-of-agents-tool","neutts-synth","openrouter-client","osv-check","patch-parser",
    "path-security","process-registry","registry","rl-training-tool","send-message-tool","session-search-tool","terminal-tool",
    "tirith-security","todo-tool","tool-backend-helpers","tool-result-storage","transcription-tools","tts-tool","url-safety",
    "vision-tools","voice-mode","web-tools","website-policy",
}

EXEC = {"terminal-tool", "code-execution-tool"}
FILES = {"file-tools", "file-operations"}
HTTP = {"web-tools", "openrouter-client", "homeassistant-tool", "transcription-tools", "tts-tool", "voice-mode", "vision-tools"}
JSONL = {"todo-tool", "tool-result-storage", "checkpoint-manager", "cronjob-tools", "rl-training-tool", "send-message-tool", "memory-tool", "session-search-tool", "approval"}
STATE = {"budget-config", "registry", "tool-backend-helpers"}
BROWSER = {"browser-tool", "browser-camofox", "browser-camofox-state", "browser-providers-base", "browser-providers-browser-use", "browser-providers-browserbase", "browser-providers-firecrawl"}
MCP = {"mcp-tool", "mcp-oauth"}
DELEGATE = {"delegate-tool", "mixture-of-agents-tool"}


def write(tool_id: str, content: str) -> None:
    (ROOT / tool_id / "tool.go").write_text(content.rstrip() + "\n", encoding="utf-8")


def common_helpers(tool_id: str) -> str:
    return f"""
func ok(action string, args map[string]any, data map[string]any) map[string]any {{
\tr := map[string]any{{"tool_id":"{tool_id}","status":"ok","action":action,"audit":audit(action,args)}}
\tfor k,v := range data {{ r[k]=v }}
\treturn r
}}
func errResp(code, msg, action string, args map[string]any) map[string]any {{
\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error_code":code,"error":msg,"action":action,"audit":audit(action,args)}}
}}
func audit(action string, args map[string]any) map[string]any {{
\treturn map[string]any{{"timestamp":time.Now().UTC().Format(time.RFC3339),"session_id":asString(args["_session_id"]),"workdir":asString(args["_workdir"]),"action":action}}
}}
func asString(v any) string {{ s,_ := v.(string); return s }}
"""


def exec_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"os/exec"
\t"runtime"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "run" }}
\tif action == "status" {{ return ok("status", args, map[string]any{{"runtime":runtime.GOOS+"/"+runtime.GOARCH}}), nil }}
\tif action != "run" {{ return errResp("INVALID_ACTION","unsupported action",action,args), nil }}
\tif p,_ := args["policy"].(map[string]any); p != nil {{
\t\tif allow,ok := p["allow_exec"].(bool); ok && !allow {{ return errResp("PERMISSION_DENIED","policy allow_exec=false",action,args), nil }}
\t}}
\tcmdText := strings.TrimSpace(asString(args["command"]))
\tif cmdText == "" {{ return errResp("VALIDATION_ERROR","missing command",action,args), nil }}
\twd := strings.TrimSpace(asString(args["_workdir"])); if wd=="" {{ wd = "." }}
\tstart := time.Now()
\tvar cmd *exec.Cmd
\tif runtime.GOOS == "windows" {{ cmd = exec.Command("cmd","/C",cmdText) }} else {{ cmd = exec.Command("sh","-lc",cmdText) }}
\tcmd.Dir = wd
\tout, runErr := cmd.CombinedOutput()
\tif runErr != nil {{
\t\treturn ok(action,args,map[string]any{{"output":string(out),"duration_ms":time.Since(start).Milliseconds(),"run_error":runErr.Error()}}), nil
\t}}
\treturn ok(action,args,map[string]any{{"output":string(out),"duration_ms":time.Since(start).Milliseconds()}}), nil
}}
{common_helpers(tool_id)}
"""


def file_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"os"
\t"path/filepath"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "list" }}
\twd := strings.TrimSpace(asString(args["_workdir"])); if wd=="" {{ wd = "." }}
\tp := strings.TrimSpace(asString(args["path"])); if p=="" {{ p = "." }}
\tt := p; if !filepath.IsAbs(t) {{ t = filepath.Join(wd,t) }}; t = filepath.Clean(t)
\tswitch action {{
\tcase "list":
\t\titems, err := os.ReadDir(t); if err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\t\trows := []map[string]any{{}}; for _,it := range items {{ rows = append(rows, map[string]any{{"name":it.Name(),"is_dir":it.IsDir()}}) }}
\t\treturn ok(action,args,map[string]any{{"path":t,"entries":rows}}), nil
\tcase "read":
\t\tb, err := os.ReadFile(t); if err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\t\treturn ok(action,args,map[string]any{{"path":t,"content":string(b)}}), nil
\tcase "write":
\t\tif err := os.MkdirAll(filepath.Dir(t),0o755); err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\t\tif err := os.WriteFile(t, []byte(asString(args["content"])), 0o644); err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\t\treturn ok(action,args,map[string]any{{"path":t,"updated_at":time.Now().UTC().Format(time.RFC3339)}}), nil
\tcase "delete":
\t\tif err := os.RemoveAll(t); err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\t\treturn ok(action,args,map[string]any{{"path":t}}), nil
\tdefault:
\t\treturn errResp("INVALID_ACTION","unsupported action",action,args), nil
\t}}
}}
{common_helpers(tool_id)}
"""


def http_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"bytes"
\t"encoding/json"
\t"io"
\t"net/http"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "request" }}
\tif action != "request" {{ return errResp("INVALID_ACTION","unsupported action",action,args), nil }}
\turl := strings.TrimSpace(asString(args["url"]))
\tif url == "" {{ return errResp("VALIDATION_ERROR","missing url",action,args), nil }}
\tmethod := strings.ToUpper(strings.TrimSpace(asString(args["method"]))); if method=="" {{ method = "GET" }}
\tbody := asString(args["body"])
\tif body == "" {{ if p,ok := args["payload"].(map[string]any); ok {{ b,_ := json.Marshal(p); body = string(b) }} }}
\treq, err := http.NewRequest(method, url, bytes.NewBufferString(body)); if err != nil {{ return errResp("VALIDATION_ERROR", err.Error(), action, args), nil }}
\tclient := &http.Client{{Timeout:30*time.Second}}
\tresp, err := client.Do(req); if err != nil {{ return errResp("NETWORK_ERROR", err.Error(), action, args), nil }}
\tdefer resp.Body.Close(); rb,_ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
\treturn ok(action,args,map[string]any{{"url":url,"status_code":resp.StatusCode,"body":string(rb)}}), nil
}}
{common_helpers(tool_id)}
"""


def jsonl_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"encoding/json"
\t"os"
\t"path/filepath"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "append" }}
\twd := strings.TrimSpace(asString(args["_workdir"])); if wd=="" {{ wd = "." }}
\tfile := filepath.Join(wd, ".heros", "data", "{tool_id}.jsonl")
\tif err := os.MkdirAll(filepath.Dir(file),0o755); err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\tif action == "list" {{
\t\tb, err := os.ReadFile(file); if err != nil && !os.IsNotExist(err) {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\t\trows := []string{{}}; for _,ln := range strings.Split(string(b),"\\n") {{ if strings.TrimSpace(ln)!="" {{ rows = append(rows, ln) }} }}
\t\treturn ok(action,args,map[string]any{{"count":len(rows),"records":rows}}), nil
\t}}
\trec := map[string]any{{"time":time.Now().UTC().Format(time.RFC3339),"payload":args}}
\tb,_ := json.Marshal(rec)
\tf,err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); if err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\tdefer f.Close(); if _,err := f.Write(append(b,'\\n')); err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\treturn ok(action,args,map[string]any{{"file":file}}), nil
}}
{common_helpers(tool_id)}
"""


def state_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"encoding/json"
\t"os"
\t"path/filepath"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "list" }}
\twd := strings.TrimSpace(asString(args["_workdir"])); if wd=="" {{ wd = "." }}
\tfile := filepath.Join(wd, ".heros", "state", "{tool_id}.json")
\tif err := os.MkdirAll(filepath.Dir(file),0o755); err != nil {{ return errResp("IO_ERROR", err.Error(), action, args), nil }}
\tst := map[string]any{{}}; if b,err := os.ReadFile(file); err==nil {{ _ = json.Unmarshal(b,&st) }}
\tkey := strings.TrimSpace(asString(args["key"]))
\tswitch action {{
\tcase "list": return ok(action,args,map[string]any{{"state":st}}), nil
\tcase "get": return ok(action,args,map[string]any{{"key":key,"value":st[key]}}), nil
\tcase "set": st[key] = args["value"]
\tcase "delete": delete(st,key)
\tdefault: return errResp("INVALID_ACTION","unsupported action",action,args), nil
\t}}
\tst["updated_at"] = time.Now().UTC().Format(time.RFC3339)
\tb,_ := json.MarshalIndent(st,\"\",\"  \"); if err := os.WriteFile(file,b,0o644); err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\treturn ok(action,args,map[string]any{{"updated":true}}), nil
}}
{common_helpers(tool_id)}
"""


def browser_or_mcp_or_delegate(tool_id: str) -> str:
    # Keep simple stateful action log but fully standardized.
    return f"""package tooldef

import (
\t"encoding/json"
\t"os"
\t"path/filepath"
\t"strings"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{ action = "status" }}
\twd := strings.TrimSpace(asString(args["_workdir"])); if wd=="" {{ wd = "." }}
\tfile := filepath.Join(wd, ".heros", "runtime", "{tool_id}.json")
\tif err := os.MkdirAll(filepath.Dir(file),0o755); err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\tst := map[string]any{{}}; if b,err := os.ReadFile(file); err==nil {{ _ = json.Unmarshal(b,&st) }}
\tif action == "status" {{ return ok(action,args,map[string]any{{"state":st}}), nil }}
\tif action == "reset" {{ st = map[string]any{{}} }} else {{ st[action] = args }}
\tst["updated_at"] = time.Now().UTC().Format(time.RFC3339)
\tb,_ := json.MarshalIndent(st,\"\",\"  \"); if err := os.WriteFile(file,b,0o644); err != nil {{ return errResp("IO_ERROR",err.Error(),action,args), nil }}
\treturn ok(action,args,map[string]any{{"updated":true}}), nil
}}
{common_helpers(tool_id)}
"""


def utility(tool_id: str) -> str:
    return f"""package tooldef

import "time"

func Execute(args map[string]any) (map[string]any, error) {{
\taction := asString(args["action"]); if action=="" {{ action = "run" }}
\treturn ok(action, args, map[string]any{{"result":"ok","timestamp":time.Now().UTC().Format(time.RFC3339)}}), nil
}}
{common_helpers(tool_id)}
"""


for tool_id in sorted(MISSING):
    if tool_id in EXEC:
        write(tool_id, exec_tool(tool_id))
    elif tool_id in FILES:
        write(tool_id, file_tool(tool_id))
    elif tool_id in HTTP:
        write(tool_id, http_tool(tool_id))
    elif tool_id in JSONL:
        write(tool_id, jsonl_tool(tool_id))
    elif tool_id in STATE:
        write(tool_id, state_tool(tool_id))
    elif tool_id in BROWSER or tool_id in MCP or tool_id in DELEGATE:
        write(tool_id, browser_or_mcp_or_delegate(tool_id))
    else:
        write(tool_id, utility(tool_id))

print("updated", len(MISSING), "tools")
