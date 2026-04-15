package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"path/filepath"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "classify"
	}
	switch action {
	case "status":
		return toolcontract.Ok("binary-extensions", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "classify":
		ext := strings.TrimSpace(asString(args["extension"]))
		path := strings.TrimSpace(asString(args["path"]))
		if ext == "" && path != "" {
			ext = filepath.Ext(path)
		}
		if ext == "" {
			return toolcontract.Error("binary-extensions", toolcontract.ErrorCodeValidationError, "missing extension or path", action, args), nil
		}
		class := classifyExt(ext)
		return toolcontract.Ok("binary-extensions", action, args, map[string]any{
			"extension": ext,
			"class":     class,
			"is_binary": class == "binary",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("binary-extensions", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func classifyExt(ext string) string {
	e := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	binary := map[string]struct{}{
		"png": {}, "jpg": {}, "jpeg": {}, "gif": {}, "webp": {}, "ico": {}, "bmp": {}, "tiff": {},
		"mp3": {}, "wav": {}, "flac": {}, "ogg": {}, "m4a": {},
		"mp4": {}, "mkv": {}, "avi": {}, "mov": {}, "webm": {},
		"zip": {}, "gz": {}, "tar": {}, "7z": {}, "rar": {},
		"exe": {}, "dll": {}, "so": {}, "dylib": {}, "bin": {}, "class": {}, "jar": {},
		"pdf": {}, "woff": {}, "woff2": {}, "ttf": {}, "otf": {},
	}
	if _, ok := binary[e]; ok {
		return "binary"
	}
	return "text"
}
