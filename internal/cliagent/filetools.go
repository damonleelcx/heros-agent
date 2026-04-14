package cliagent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func argBool(args map[string]any, key string, def bool) bool {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		switch s {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		default:
			return def
		}
	default:
		return def
	}
}

func argInt(args map[string]any, key string, def int) int {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &n); err == nil {
			return n
		}
		return def
	default:
		return def
	}
}

func resolveUserPath(workDir, p string) (string, error) {
	raw := strings.TrimSpace(p)
	if raw == "" {
		return "", fmt.Errorf("missing path")
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		wd = "."
	}
	return filepath.Clean(filepath.Join(wd, raw)), nil
}

type fileEntry struct {
	RelPath string `json:"rel_path"`
	AbsPath string `json:"abs_path"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
}

func listFilesJSON(workDir string, args map[string]any) (string, error) {
	target := ArgString(args, "path")
	if target == "" {
		target = "."
	}
	abs, err := resolveUserPath(workDir, target)
	if err != nil {
		return "", err
	}
	recursive := argBool(args, "recursive", false)
	maxEntries := argInt(args, "max_entries", 200)
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if maxEntries > 5000 {
		maxEntries = 5000
	}

	var out []fileEntry
	appendEntry := func(path string, d fs.DirEntry) error {
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			rel = d.Name()
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		typ := "file"
		if d.IsDir() {
			typ = "dir"
		}
		var size int64
		if fi, ierr := d.Info(); ierr == nil {
			size = fi.Size()
		}
		out = append(out, fileEntry{
			RelPath: rel,
			AbsPath: filepath.Clean(path),
			Type:    typ,
			Size:    size,
		})
		if len(out) >= maxEntries {
			return fs.SkipAll
		}
		return nil
	}

	if recursive {
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			return appendEntry(path, d)
		})
		if err != nil && err != fs.SkipAll {
			return "", err
		}
	} else {
		entries, derr := os.ReadDir(abs)
		if derr != nil {
			return "", derr
		}
		for _, d := range entries {
			if err := appendEntry(filepath.Join(abs, d.Name()), d); err == fs.SkipAll {
				break
			} else if err != nil {
				return "", err
			}
		}
	}
	resp := map[string]any{
		"path":        filepath.Clean(abs),
		"recursive":   recursive,
		"max_entries": maxEntries,
		"entries":     out,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

func readFileJSON(workDir string, args map[string]any) (string, error) {
	p := ArgString(args, "path")
	abs, err := resolveUserPath(workDir, p)
	if err != nil {
		return "", err
	}
	enc := strings.ToLower(strings.TrimSpace(ArgString(args, "encoding")))
	if enc == "" {
		enc = "text"
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := ""
	actual := enc
	switch enc {
	case "base64":
		content = base64.StdEncoding.EncodeToString(b)
	case "text":
		if utf8.Valid(b) {
			content = string(b)
		} else {
			content = base64.StdEncoding.EncodeToString(b)
			actual = "base64"
		}
	default:
		return "", fmt.Errorf("unsupported encoding %q (use text or base64)", enc)
	}
	resp := map[string]any{
		"path":     filepath.Clean(abs),
		"encoding": actual,
		"size":     len(b),
		"content":  content,
	}
	bj, _ := json.Marshal(resp)
	return string(bj), nil
}

func writeFileJSON(workDir string, args map[string]any) (string, error) {
	p := ArgString(args, "path")
	abs, err := resolveUserPath(workDir, p)
	if err != nil {
		return "", err
	}
	content := ArgString(args, "content")
	if content == "" && ArgString(args, "content") == "" {
		// Keep empty-string write valid, but require key exists.
		if _, ok := args["content"]; !ok {
			return "", fmt.Errorf("missing content")
		}
	}
	enc := strings.ToLower(strings.TrimSpace(ArgString(args, "encoding")))
	if enc == "" {
		enc = "text"
	}
	appendMode := argBool(args, "append", false)
	createDirs := argBool(args, "create_dirs", true)
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
	}
	var data []byte
	switch enc {
	case "text":
		data = []byte(content)
	case "base64":
		dec, derr := base64.StdEncoding.DecodeString(content)
		if derr != nil {
			return "", fmt.Errorf("invalid base64 content: %w", derr)
		}
		data = dec
	default:
		return "", fmt.Errorf("unsupported encoding %q (use text or base64)", enc)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(abs, flag, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	resp := map[string]any{
		"path":    filepath.Clean(abs),
		"written": len(data),
		"append":  appendMode,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

func deletePathJSON(workDir string, args map[string]any) (string, error) {
	p := ArgString(args, "path")
	abs, err := resolveUserPath(workDir, p)
	if err != nil {
		return "", err
	}
	recursive := argBool(args, "recursive", false)
	if recursive {
		if err := os.RemoveAll(abs); err != nil {
			return "", err
		}
	} else {
		if err := os.Remove(abs); err != nil {
			return "", err
		}
	}
	resp := map[string]any{
		"path":      filepath.Clean(abs),
		"deleted":   true,
		"recursive": recursive,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

func makeDirJSON(workDir string, args map[string]any) (string, error) {
	p := ArgString(args, "path")
	abs, err := resolveUserPath(workDir, p)
	if err != nil {
		return "", err
	}
	recursive := argBool(args, "recursive", true)
	if recursive {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", err
		}
	} else {
		if err := os.Mkdir(abs, 0o755); err != nil {
			return "", err
		}
	}
	resp := map[string]any{
		"path":      filepath.Clean(abs),
		"created":   true,
		"recursive": recursive,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}
