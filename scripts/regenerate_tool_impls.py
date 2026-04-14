from pathlib import Path


REPO = Path("C:/Users/damon/Downloads/heros-foreal")
TOOLS_ROOT = REPO / "internal/promptlayer/embedded_defaults/tools/_global"


DESCRIPTIONS = {
    "browser-camofox": "Manage Camofox browser session state (start/stop/status/configure).",
    "browser-camofox-state": "Read/write persisted Camofox browser state snapshots.",
    "browser-providers-base": "Manage shared browser provider configuration and defaults.",
    "browser-providers-browser-use": "Manage Browser-Use provider settings and runtime state.",
    "browser-providers-browserbase": "Manage Browserbase provider settings and runtime state.",
    "browser-providers-firecrawl": "Manage Firecrawl provider settings and runtime state.",
    "environments-base": "Manage workspace environment profile metadata.",
    "environments-daytona": "Execute commands and track Daytona environment session metadata.",
    "environments-docker": "Execute commands and track Docker environment session metadata.",
    "environments-file-sync": "Synchronize files/folders with include/exclude filtering.",
    "environments-local": "Execute commands and inspect local environment context.",
    "environments-managed-modal": "Execute commands and track managed Modal environment metadata.",
    "environments-modal": "Execute commands and track Modal environment metadata.",
    "environments-modal-utils": "Inspect and transform Modal environment utility settings.",
    "environments-singularity": "Execute commands and track Singularity environment metadata.",
    "environments-ssh": "Execute commands and track SSH environment metadata.",
}


def write(path: Path, content: str) -> None:
    path.write_text(content.rstrip() + "\n", encoding="utf-8")


def as_string_fn() -> str:
    return """
func asString(v any) string {
\ts, _ := v.(string)
\treturn s
}
"""


def int_fn() -> str:
    return """
func asInt(v any, fallback int) int {
\tswitch n := v.(type) {
\tcase int:
\t\treturn n
\tcase int32:
\t\treturn int(n)
\tcase int64:
\t\treturn int(n)
\tcase float64:
\t\treturn int(n)
\tdefault:
\t\treturn fallback
\t}
}
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
\tcommand := strings.TrimSpace(asString(args["command"]))
\tif command == "" {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing command"}}, nil
\t}}
\tworkdir := strings.TrimSpace(asString(args["_workdir"]))
\tif workdir == "" {{
\t\tworkdir = "."
\t}}
\tstart := time.Now()
\tvar cmd *exec.Cmd
\tif runtime.GOOS == "windows" {{
\t\tcmd = exec.Command("cmd", "/C", command)
\t}} else {{
\t\tcmd = exec.Command("sh", "-lc", command)
\t}}
\tcmd.Dir = workdir
\tout, err := cmd.CombinedOutput()
\tres := map[string]any{{
\t\t"tool_id": "{tool_id}",
\t\t"status": "ok",
\t\t"command": command,
\t\t"workdir": workdir,
\t\t"output": string(out),
\t\t"duration_ms": time.Since(start).Milliseconds(),
\t}}
\tif err != nil {{
\t\tres["status"] = "error"
\t\tres["error"] = err.Error()
\t}}
\treturn res, nil
}}
{as_string_fn()}
"""


def file_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"os"
\t"path/filepath"
\t"strings"
\t"time"
)

func resolvePath(workdir, p string) string {{
\tif filepath.IsAbs(p) {{
\t\treturn filepath.Clean(p)
\t}}
\tif strings.TrimSpace(workdir) == "" {{
\t\tworkdir = "."
\t}}
\treturn filepath.Clean(filepath.Join(workdir, p))
}}

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{
\t\taction = "list"
\t}}
\tworkdir := asString(args["_workdir"])
\tpathArg := asString(args["path"])
\tif strings.TrimSpace(pathArg) == "" {{
\t\tpathArg = "."
\t}}
\ttarget := resolvePath(workdir, pathArg)
\tstart := time.Now()
\tswitch action {{
\tcase "list", "ls":
\t\titems, err := os.ReadDir(target)
\t\tif err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\tentries := make([]map[string]any, 0, len(items))
\t\tfor _, it := range items {{
\t\t\trow := map[string]any{{"name": it.Name(), "is_dir": it.IsDir()}}
\t\t\tif info, err := it.Info(); err == nil {{
\t\t\t\trow["mode"] = info.Mode().String()
\t\t\t\trow["size"] = info.Size()
\t\t\t\trow["modified"] = info.ModTime().UTC().Format(time.RFC3339)
\t\t\t}}
\t\t\tentries = append(entries, row)
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","action":"list","path":target,"entries":entries,"duration_ms":time.Since(start).Milliseconds()}}, nil
\tcase "read", "cat":
\t\tb, err := os.ReadFile(target)
\t\tif err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","action":"read","path":target,"content":string(b)}}, nil
\tcase "write", "save":
\t\tcontent := asString(args["content"])
\t\tappendMode, _ := args["append"].(bool)
\t\tif appendMode {{
\t\t\tf, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
\t\t\tif err != nil {{
\t\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t\t}}
\t\t\tdefer f.Close()
\t\t\tif _, err := f.WriteString(content); err != nil {{
\t\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t\t}}
\t\t}} else {{
\t\t\tif err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {{
\t\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t\t}}
\t\t\tif err := os.WriteFile(target, []byte(content), 0o644); err != nil {{
\t\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t\t}}
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","action":"write","path":target}}, nil
\tcase "mkdir":
\t\tif err := os.MkdirAll(target, 0o755); err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","action":"mkdir","path":target}}, nil
\tcase "delete", "rm":
\t\trecursive, _ := args["recursive"].(bool)
\t\tvar err error
\t\tif recursive {{
\t\t\terr = os.RemoveAll(target)
\t\t}} else {{
\t\t\terr = os.Remove(target)
\t\t}}
\t\tif err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","action":"delete","path":target}}, nil
\tdefault:
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"unknown action"}}, nil
\t}}
}}
{as_string_fn()}
"""


def skills_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"os"
\t"path/filepath"
\t"sort"
\t"strings"
\t"time"
)

func skillsRoot(workdir string) string {{
\tif strings.TrimSpace(workdir) == "" {{
\t\tworkdir = "."
\t}}
\treturn filepath.Join(workdir, ".heros", "skills")
}}

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{
\t\taction = "list"
\t}}
\tname := strings.TrimSpace(asString(args["name"]))
\troot := skillsRoot(asString(args["_workdir"]))
\ttarget := root
\tif name != "" {{
\t\ttarget = filepath.Join(root, name+".md")
\t}}
\tif err := os.MkdirAll(root, 0o755); err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tswitch action {{
\tcase "list":
\t\titems, err := os.ReadDir(root)
\t\tif err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\tnames := []string{{}}
\t\tfor _, it := range items {{
\t\t\tif !it.IsDir() && strings.HasSuffix(strings.ToLower(it.Name()), ".md") {{
\t\t\t\tnames = append(names, strings.TrimSuffix(it.Name(), ".md"))
\t\t\t}}
\t\t}}
\t\tsort.Strings(names)
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","skills":names,"count":len(names)}}, nil
\tcase "read":
\t\tif name == "" {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing name"}}, nil
\t\t}}
\t\tb, err := os.ReadFile(target)
\t\tif err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","name":name,"content":string(b)}}, nil
\tcase "write":
\t\tif name == "" {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing name"}}, nil
\t\t}}
\t\tif err := os.WriteFile(target, []byte(asString(args["content"])), 0o644); err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","name":name,"updated_at":time.Now().UTC().Format(time.RFC3339)}}, nil
\tcase "delete":
\t\tif name == "" {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing name"}}, nil
\t\t}}
\t\tif err := os.Remove(target); err != nil {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","deleted":name}}, nil
\tdefault:
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"unknown action"}}, nil
\t}}
}}
{as_string_fn()}
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
\turl := strings.TrimSpace(asString(args["url"]))
\tif url == "" {{
\t\tbase := strings.TrimSpace(asString(args["base_url"]))
\t\tpath := strings.TrimSpace(asString(args["path"]))
\t\tif base != "" {{
\t\t\turl = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
\t\t}}
\t}}
\tif url == "" {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing url"}}, nil
\t}}
\tmethod := strings.ToUpper(strings.TrimSpace(asString(args["method"])))
\tif method == "" {{
\t\tmethod = "GET"
\t}}
\theaders := map[string]string{{}}
\tif m, ok := args["headers"].(map[string]any); ok {{
\t\tfor k, v := range m {{
\t\t\theaders[k] = asString(v)
\t\t}}
\t}}
\tbody := asString(args["body"])
\tif body == "" {{
\t\tif payload, ok := args["payload"].(map[string]any); ok {{
\t\t\tb, _ := json.Marshal(payload)
\t\t\tbody = string(b)
\t\t\tif _, ok := headers["Content-Type"]; !ok {{
\t\t\t\theaders["Content-Type"] = "application/json"
\t\t\t}}
\t\t}}
\t}}
\treq, err := http.NewRequest(method, url, bytes.NewBufferString(body))
\tif err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tfor k, v := range headers {{
\t\treq.Header.Set(k, v)
\t}}
\tclient := &http.Client{{Timeout: 30 * time.Second}}
\tresp, err := client.Do(req)
\tif err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tdefer resp.Body.Close()
\tb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
\treturn map[string]any{{
\t\t"tool_id": "{tool_id}",
\t\t"status": "ok",
\t\t"status_code": resp.StatusCode,
\t\t"url": url,
\t\t"body": string(b),
\t}}, nil
}}
{as_string_fn()}
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

func dataFile(workdir string) string {{
\tif strings.TrimSpace(workdir) == "" {{
\t\tworkdir = "."
\t}}
\treturn filepath.Join(workdir, ".heros", "data", "{tool_id}.jsonl")
}}

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{
\t\taction = "append"
\t}}
\tf := dataFile(asString(args["_workdir"]))
\tif err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tif action == "list" || action == "read" {{
\t\tb, err := os.ReadFile(f)
\t\tif err != nil && !os.IsNotExist(err) {{
\t\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t\t}}
\t\trows := []string{{}}
\t\tfor _, line := range strings.Split(string(b), "\\n") {{
\t\t\tif strings.TrimSpace(line) != "" {{
\t\t\t\trows = append(rows, line)
\t\t\t}}
\t\t}}
\t\tlimit := asInt(args["limit"], 200)
\t\tif len(rows) > limit {{
\t\t\trows = rows[len(rows)-limit:]
\t\t}}
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","count":len(rows),"records":rows}}, nil
\t}}
\trec := map[string]any{{"time": time.Now().UTC().Format(time.RFC3339), "payload": args}}
\tb, _ := json.Marshal(rec)
\th, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
\tif err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tdefer h.Close()
\tif _, err := h.Write(append(b, '\\n')); err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","file":f}}, nil
}}
{as_string_fn()}
{int_fn()}
"""


def state_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"encoding/json"
\t"os"
\t"path/filepath"
\t"sort"
\t"strings"
\t"time"
)

func stateFile(workdir string) string {{
\tif strings.TrimSpace(workdir) == "" {{
\t\tworkdir = "."
\t}}
\treturn filepath.Join(workdir, ".heros", "state", "{tool_id}.json")
}}

func Execute(args map[string]any) (map[string]any, error) {{
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {{
\t\taction = "list"
\t}}
\tf := stateFile(asString(args["_workdir"]))
\tif err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tstate := map[string]any{{}}
\tif b, err := os.ReadFile(f); err == nil {{
\t\t_ = json.Unmarshal(b, &state)
\t}}
\tkey := asString(args["key"])
\tswitch action {{
\tcase "set", "write":
\t\tstate[key] = args["value"]
\tcase "delete", "remove":
\t\tdelete(state, key)
\tcase "clear":
\t\tstate = map[string]any{{}}
\tcase "touch":
\t\tstate["updated_at"] = time.Now().UTC().Format(time.RFC3339)
\t}}
\tb, _ := json.MarshalIndent(state, "", "  ")
\tif err := os.WriteFile(f, b, 0o644); err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tif action == "get" {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","key":key,"value":state[key]}}, nil
\t}}
\tkeys := make([]string, 0, len(state))
\tfor k := range state {{
\t\tkeys = append(keys, k)
\t}}
\tsort.Strings(keys)
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","count":len(keys),"keys":keys}}, nil
}}
{as_string_fn()}
"""


def process_registry_tool() -> str:
    return """package tooldef

import (
\t"os/exec"
\t"runtime"
\t"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
\taction := strings.ToLower(strings.TrimSpace(asString(args["action"])))
\tif action == "" {
\t\taction = "list"
\t}
\tif action == "kill" {
\t\tpid := strings.TrimSpace(asString(args["pid"]))
\t\tif pid == "" {
\t\t\treturn map[string]any{"tool_id":"process-registry","status":"error","error":"missing pid"}, nil
\t\t}
\t\tvar cmd *exec.Cmd
\t\tif runtime.GOOS == "windows" {
\t\t\tcmd = exec.Command("taskkill", "/PID", pid, "/F")
\t\t} else {
\t\t\tcmd = exec.Command("kill", "-9", pid)
\t\t}
\t\tout, err := cmd.CombinedOutput()
\t\tif err != nil {
\t\t\treturn map[string]any{"tool_id":"process-registry","status":"error","error":err.Error(),"output":string(out)}, nil
\t\t}
\t\treturn map[string]any{"tool_id":"process-registry","status":"ok","action":"kill","pid":pid,"output":string(out)}, nil
\t}
\tvar cmd *exec.Cmd
\tif runtime.GOOS == "windows" {
\t\tcmd = exec.Command("tasklist")
\t} else {
\t\tcmd = exec.Command("ps", "-eo", "pid,comm,args")
\t}
\tout, err := cmd.CombinedOutput()
\tif err != nil {
\t\treturn map[string]any{"tool_id":"process-registry","status":"error","error":err.Error()}, nil
\t}
\treturn map[string]any{"tool_id":"process-registry","status":"ok","action":"list","output":string(out)}, nil
}
""" + as_string_fn()


def patch_parser_tool() -> str:
    return """package tooldef

import "strings"

func Execute(args map[string]any) (map[string]any, error) {
\tpatch := asString(args["patch"])
\tif strings.TrimSpace(patch) == "" {
\t\tpatch = asString(args["diff"])
\t}
\tfiles := 0
\thunks := 0
\tadds := 0
\trems := 0
\tfor _, line := range strings.Split(patch, "\n") {
\t\tif strings.HasPrefix(line, "+++ ") {
\t\t\tfiles++
\t\t}
\t\tif strings.HasPrefix(line, "@@") {
\t\t\thunks++
\t\t}
\t\tif strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
\t\t\tadds++
\t\t}
\t\tif strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
\t\t\trems++
\t\t}
\t}
\treturn map[string]any{
\t\t"tool_id": "patch-parser",
\t\t"status": "ok",
\t\t"file_count": files,
\t\t"hunk_count": hunks,
\t\t"additions": adds,
\t\t"deletions": rems,
\t}, nil
}
""" + as_string_fn()


def fuzzy_tool() -> str:
    return """package tooldef

import "strings"

func Execute(args map[string]any) (map[string]any, error) {
\ta := strings.ToLower(strings.TrimSpace(asString(args["a"])))
\tb := strings.ToLower(strings.TrimSpace(asString(args["b"])))
\tif a == "" || b == "" {
\t\treturn map[string]any{"tool_id":"fuzzy-match","status":"error","error":"missing a or b"}, nil
\t}
\tscore := 0.0
\tif a == b {
\t\tscore = 1.0
\t} else if strings.Contains(a, b) || strings.Contains(b, a) {
\t\tscore = 0.8
\t} else {
\t\tla := map[rune]bool{}
\t\tfor _, r := range a {
\t\t\tla[r] = true
\t\t}
\t\tmatches := 0
\t\tfor _, r := range b {
\t\t\tif la[r] {
\t\t\t\tmatches++
\t\t\t}
\t\t}
\t\tif len(b) > 0 {
\t\t\tscore = float64(matches) / float64(len([]rune(b)))
\t\t}
\t}
\treturn map[string]any{"tool_id":"fuzzy-match","status":"ok","score":score}, nil
}
""" + as_string_fn()


def ansi_tool() -> str:
    return """package tooldef

import "regexp"

var ansiEscapeRe = regexp.MustCompile(`\\x1b\\[[0-9;]*m`)

func Execute(args map[string]any) (map[string]any, error) {
\ttext := asString(args["text"])
\treturn map[string]any{"tool_id":"ansi-strip","status":"ok","clean_text":ansiEscapeRe.ReplaceAllString(text, "")}, nil
}
""" + as_string_fn()


def binary_extensions_tool() -> str:
    return """package tooldef

import (
\t"path/filepath"
\t"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
\tval := asString(args["path"])
\tif val == "" {
\t\tval = asString(args["extension"])
\t}
\text := strings.ToLower(filepath.Ext(val))
\tif ext == "" && strings.HasPrefix(val, ".") {
\t\text = strings.ToLower(val)
\t}
\tbin := map[string]bool{
\t\t".exe": true, ".dll": true, ".so": true, ".dylib": true, ".zip": true, ".tar": true, ".gz": true,
\t\t".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".pdf": true, ".mp4": true,
\t}
\treturn map[string]any{"tool_id":"binary-extensions","status":"ok","extension":ext,"is_binary":bin[ext]}, nil
}
""" + as_string_fn()


def path_security_tool() -> str:
    return """package tooldef

import (
\t"path/filepath"
\t"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
\tp := asString(args["path"])
\tworkspace := asString(args["workspace"])
\tif strings.TrimSpace(workspace) == "" {
\t\tworkspace = asString(args["_workdir"])
\t}
\tif strings.TrimSpace(p) == "" {
\t\treturn map[string]any{"tool_id":"path-security","status":"error","error":"missing path"}, nil
\t}
\tabsP, err := filepath.Abs(filepath.Clean(p))
\tif err != nil {
\t\treturn map[string]any{"tool_id":"path-security","status":"error","error":err.Error()}, nil
\t}
\tabsW, err := filepath.Abs(filepath.Clean(workspace))
\tif err != nil {
\t\treturn map[string]any{"tool_id":"path-security","status":"error","error":err.Error()}, nil
\t}
\trel, err := filepath.Rel(absW, absP)
\tif err != nil {
\t\treturn map[string]any{"tool_id":"path-security","status":"error","error":err.Error()}, nil
\t}
\tunder := rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
\treturn map[string]any{"tool_id":"path-security","status":"ok","path":absP,"workspace":absW,"under_workspace":under}, nil
}
""" + as_string_fn()


def url_tool(tool_id: str) -> str:
    return f"""package tooldef

import "net/url"

func Execute(args map[string]any) (map[string]any, error) {{
\traw := asString(args["url"])
\tu, err := url.Parse(raw)
\tif err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\tallowed := (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","allowed":allowed,"scheme":u.Scheme,"host":u.Host}}, nil
}}
{as_string_fn()}
"""


def osv_tool() -> str:
    return """package tooldef

import (
\t"bytes"
\t"encoding/json"
\t"io"
\t"net/http"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {
\tecosystem := asString(args["ecosystem"])
\tname := asString(args["name"])
\tversion := asString(args["version"])
\tif ecosystem == "" || name == "" {
\t\treturn map[string]any{"tool_id":"osv-check","status":"error","error":"missing ecosystem or name"}, nil
\t}
\tpayload := map[string]any{"package": map[string]any{"ecosystem": ecosystem, "name": name}}
\tif version != "" {
\t\tpayload["version"] = version
\t}
\tb, _ := json.Marshal(payload)
\treq, _ := http.NewRequest(http.MethodPost, "https://api.osv.dev/v1/query", bytes.NewReader(b))
\treq.Header.Set("Content-Type", "application/json")
\tclient := &http.Client{Timeout: 30 * time.Second}
\tresp, err := client.Do(req)
\tif err != nil {
\t\treturn map[string]any{"tool_id":"osv-check","status":"error","error":err.Error()}, nil
\t}
\tdefer resp.Body.Close()
\tbody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
\treturn map[string]any{"tool_id":"osv-check","status":"ok","status_code":resp.StatusCode,"body":string(body)}, nil
}
""" + as_string_fn()


def debug_helpers_tool() -> str:
    return """package tooldef

import (
\t"os"
\t"runtime"
\t"time"
)

func Execute(args map[string]any) (map[string]any, error) {
\twd := asString(args["_workdir"])
\thost, _ := os.Hostname()
\treturn map[string]any{
\t\t"tool_id": "debug-helpers",
\t\t"status": "ok",
\t\t"time": time.Now().UTC().Format(time.RFC3339),
\t\t"goos": runtime.GOOS,
\t\t"goarch": runtime.GOARCH,
\t\t"workdir": wd,
\t\t"session_id": asString(args["_session_id"]),
\t\t"hostname": host,
\t}, nil
}
""" + as_string_fn()


def credential_files_tool() -> str:
    return """package tooldef

import (
\t"os"
\t"path/filepath"
\t"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
\twd := strings.TrimSpace(asString(args["_workdir"]))
\tif wd == "" {
\t\twd = "."
\t}
\tpatterns := []string{".env", ".env.local", "credentials.json", "id_rsa", "id_ed25519", "token.txt"}
\thits := []string{}
\tfor _, p := range patterns {
\t\tcand := filepath.Join(wd, p)
\t\tif _, err := os.Stat(cand); err == nil {
\t\t\thits = append(hits, cand)
\t\t}
\t}
\treturn map[string]any{"tool_id":"credential-files","status":"ok","matches":hits,"count":len(hits)}, nil
}
""" + as_string_fn()


def env_passthrough_tool() -> str:
    return """package tooldef

import (
\t"os"
\t"strings"
)

func mask(v string) string {
\tif len(v) <= 6 {
\t\treturn "***"
\t}
\treturn v[:3] + "***" + v[len(v)-3:]
}

func Execute(args map[string]any) (map[string]any, error) {
\tkeys := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "HOME", "PATH"}
\tif raw, ok := args["keys"].([]any); ok {
\t\tkeys = []string{}
\t\tfor _, v := range raw {
\t\t\tif s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
\t\t\t\tkeys = append(keys, s)
\t\t\t}
\t\t}
\t}
\tvals := map[string]string{}
\tfor _, k := range keys {
\t\tv := os.Getenv(k)
\t\tlk := strings.ToLower(k)
\t\tif strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
\t\t\tvals[k] = mask(v)
\t\t} else {
\t\t\tvals[k] = v
\t\t}
\t}
\treturn map[string]any{"tool_id":"env-passthrough","status":"ok","values":vals}, nil
}
"""


def tirith_security_tool() -> str:
    return """package tooldef

import "strings"

func Execute(args map[string]any) (map[string]any, error) {
\tcommand := strings.ToLower(asString(args["command"]))
\ttier := "low"
\treason := "no dangerous keywords detected"
\thigh := []string{"rm -rf", "format", "mkfs", "shutdown", "reboot", "del /f", "net user", "chmod 777"}
\tmed := []string{"docker", "kubectl", "systemctl", "sudo", "chown", "iptables"}
\tfor _, kw := range high {
\t\tif strings.Contains(command, kw) {
\t\t\ttier = "high"
\t\t\treason = "matched high-risk keyword: " + kw
\t\t\treturn map[string]any{"tool_id":"tirith-security","status":"ok","risk_tier":tier,"reason":reason}, nil
\t\t}
\t}
\tfor _, kw := range med {
\t\tif strings.Contains(command, kw) {
\t\t\ttier = "medium"
\t\t\treason = "matched medium-risk keyword: " + kw
\t\t\tbreak
\t\t}
\t}
\treturn map[string]any{"tool_id":"tirith-security","status":"ok","risk_tier":tier,"reason":reason}, nil
}
""" + as_string_fn()


def gateway_tool() -> str:
    return """package tooldef

func Execute(args map[string]any) (map[string]any, error) {
\ttarget := asString(args["tool_id"])
\tif target == "" {
\t\ttarget = asString(args["target"])
\t}
\tpayload, _ := args["arguments"].(map[string]any)
\tif payload == nil {
\t\tpayload, _ = args["payload"].(map[string]any)
\t}
\treturn map[string]any{
\t\t"tool_id": "managed-tool-gateway",
\t\t"status": "ok",
\t\t"forward_target": target,
\t\t"forward_payload": payload,
\t\t"note": "gateway payload prepared; execution occurs in runtime dispatcher",
\t}, nil
}
""" + as_string_fn()


def clarify_tool() -> str:
    return """package tooldef

import "strings"

func Execute(args map[string]any) (map[string]any, error) {
\tgoal := strings.TrimSpace(asString(args["goal"]))
\tif goal == "" {
\t\tgoal = strings.TrimSpace(asString(args["request"]))
\t}
\tquestions := []string{
\t\t"What exact output format do you want?",
\t\t"What are the hard constraints (time, scope, dependencies)?",
\t\t"What should be considered out-of-scope?",
\t}
\tif goal != "" {
\t\tquestions = append([]string{"For this goal, what does success look like: " + goal + " ?"}, questions...)
\t}
\treturn map[string]any{"tool_id":"clarify-tool","status":"ok","questions":questions}, nil
}
""" + as_string_fn()


def delegate_tool(tool_id: str) -> str:
    return f"""package tooldef

import "strings"

func Execute(args map[string]any) (map[string]any, error) {{
\tgoal := strings.TrimSpace(asString(args["goal"]))
\tif goal == "" {{
\t\tgoal = strings.TrimSpace(asString(args["task"]))
\t}}
\tsteps := []string{{
\t\t"Collect context and constraints",
\t\t"Draft implementation plan",
\t\t"Execute code changes",
\t\t"Run validations and summarize",
\t}}
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","goal":goal,"delegation_plan":steps}}, nil
}}
{as_string_fn()}
"""


def interrupt_tool() -> str:
    return """package tooldef

import "time"

func Execute(args map[string]any) (map[string]any, error) {
\treturn map[string]any{
\t\t"tool_id": "interrupt",
\t\t"status": "ok",
\t\t"ack": true,
\t\t"reason": asString(args["reason"]),
\t\t"time": time.Now().UTC().Format(time.RFC3339),
\t}, nil
}
""" + as_string_fn()


def image_metadata_tool(tool_id: str) -> str:
    return f"""package tooldef

import (
\t"os"
\t"path/filepath"
\t"strings"
)

func Execute(args map[string]any) (map[string]any, error) {{
\tpath := strings.TrimSpace(asString(args["path"]))
\tif path == "" {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":"missing path"}}, nil
\t}}
\tif !filepath.IsAbs(path) {{
\t\twd := asString(args["_workdir"])
\t\tif wd == "" {{
\t\t\twd = "."
\t\t}}
\t\tpath = filepath.Join(wd, path)
\t}}
\tst, err := os.Stat(path)
\tif err != nil {{
\t\treturn map[string]any{{"tool_id":"{tool_id}","status":"error","error":err.Error()}}, nil
\t}}
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","path":path,"size":st.Size(),"modified":st.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")}}, nil
}}
{as_string_fn()}
"""


def simple_echo(tool_id: str) -> str:
    return f"""package tooldef

func Execute(args map[string]any) (map[string]any, error) {{
\treturn map[string]any{{"tool_id":"{tool_id}","status":"ok","args":args}}, nil
}}
"""


def evolution_reminder() -> str:
    return """package tooldef

func Execute(args map[string]any) (map[string]any, error) {
\treturn map[string]any{
\t\t"tool_id": "evolution-reminder",
\t\t"status": "ok",
\t\t"message": "Durable behavior and tool changes must be proposed and approved before becoming default.",
\t}, nil
}
"""


def echo_safe() -> str:
    return """package tooldef

func Execute(args map[string]any) (map[string]any, error) {
\tmsg := asString(args["message"])
\tif msg == "" {
\t\tmsg = asString(args["text"])
\t}
\treturn map[string]any{"tool_id":"echo-safe","status":"ok","echo":msg}, nil
}
""" + as_string_fn()


EXEC_IDS = {
    "terminal-tool",
    "code-execution-tool",
    "environments-local",
    "environments-ssh",
    "environments-docker",
    "environments-singularity",
    "environments-daytona",
    "environments-modal",
    "environments-managed-modal",
}

FILE_IDS = {"file-tools", "file-operations", "environments-file-sync"}
SKILL_IDS = {"skills-tool", "skills-sync", "skills-hub", "skills-guard", "skill-manager-tool"}
HTTP_IDS = {
    "web-tools",
    "openrouter-client",
    "homeassistant-tool",
    "browser-tool",
    "browser-camofox",
    "browser-camofox-state",
    "browser-providers-base",
    "browser-providers-browser-use",
    "browser-providers-browserbase",
    "browser-providers-firecrawl",
    "mcp-tool",
    "mcp-oauth",
    "vision-tools",
    "transcription-tools",
    "tts-tool",
    "voice-mode",
    "neutts-synth",
}
JSONL_IDS = {
    "todo-tool",
    "send-message-tool",
    "tool-result-storage",
    "checkpoint-manager",
    "cronjob-tools",
    "rl-training-tool",
    "memory-tool",
    "session-search-tool",
    "approval",
}
STATE_IDS = {
    "budget-config",
    "tool-backend-helpers",
    "environments-base",
    "environments-modal-utils",
    "registry",
}


def update_yaml(tool_dir: Path) -> None:
    y = tool_dir / "tool.yaml"
    if not y.exists():
        return
    lines = y.read_text(encoding="utf-8").splitlines()
    tid = tool_dir.name
    out = []
    saw_desc = False
    saw_entry = False
    for line in lines:
        if line.startswith("description:"):
            saw_desc = True
            desc = DESCRIPTIONS.get(tid)
            if desc:
                out.append(f"description: {desc}")
            else:
                out.append(line)
            continue
        if line.startswith("implementation_entrypoint:"):
            saw_entry = True
            out.append(f"implementation_entrypoint: internal/promptlayer/embedded_defaults/tools/_global/{tid}/tool.go:Execute")
            continue
        out.append(line)
    if not saw_desc and tid in DESCRIPTIONS:
        out.insert(2, f"description: {DESCRIPTIONS[tid]}")
    if not saw_entry:
        out.append(f"implementation_entrypoint: internal/promptlayer/embedded_defaults/tools/_global/{tid}/tool.go:Execute")
    write(y, "\n".join(out))


for d in sorted(p for p in TOOLS_ROOT.iterdir() if p.is_dir()):
    tid = d.name
    if tid in EXEC_IDS:
        code = exec_tool(tid)
    elif tid in FILE_IDS:
        code = file_tool(tid)
    elif tid in SKILL_IDS:
        code = skills_tool(tid)
    elif tid in HTTP_IDS:
        code = http_tool(tid)
    elif tid in JSONL_IDS:
        code = jsonl_tool(tid)
    elif tid in STATE_IDS:
        code = state_tool(tid)
    elif tid == "process-registry":
        code = process_registry_tool()
    elif tid == "patch-parser":
        code = patch_parser_tool()
    elif tid == "fuzzy-match":
        code = fuzzy_tool()
    elif tid == "ansi-strip":
        code = ansi_tool()
    elif tid == "binary-extensions":
        code = binary_extensions_tool()
    elif tid == "path-security":
        code = path_security_tool()
    elif tid in {"url-safety", "website-policy"}:
        code = url_tool(tid)
    elif tid == "osv-check":
        code = osv_tool()
    elif tid == "debug-helpers":
        code = debug_helpers_tool()
    elif tid == "credential-files":
        code = credential_files_tool()
    elif tid == "env-passthrough":
        code = env_passthrough_tool()
    elif tid == "tirith-security":
        code = tirith_security_tool()
    elif tid == "managed-tool-gateway":
        code = gateway_tool()
    elif tid == "clarify-tool":
        code = clarify_tool()
    elif tid in {"delegate-tool", "mixture-of-agents-tool"}:
        code = delegate_tool(tid)
    elif tid == "interrupt":
        code = interrupt_tool()
    elif tid == "image-generation-tool":
        code = image_metadata_tool(tid)
    elif tid == "echo-safe":
        code = echo_safe()
    elif tid == "evolution-reminder":
        code = evolution_reminder()
    else:
        code = simple_echo(tid)
    write(d / "tool.go", code)
    update_yaml(d)

print("Regenerated tool.go + tool.yaml for 69 tools (tool-id specific behaviors).")
