package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "inspect"
	}
	switch action {
	case "status":
		return toolcontract.Ok("image-generation-tool", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "inspect":
		path := strings.TrimSpace(asString(args["path"]))
		if path == "" {
			return toolcontract.Error("image-generation-tool", toolcontract.ErrorCodeValidationError, "missing path", action, args), nil
		}
		st, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return toolcontract.Error("image-generation-tool", toolcontract.ErrorCodeNotFound, err.Error(), action, args), nil
			}
			return toolcontract.Error("image-generation-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		isImage := isImageExt(ext)
		return toolcontract.Ok("image-generation-tool", action, args, map[string]any{
			"path":        path,
			"extension":   ext,
			"is_image":    isImage,
			"size_bytes":  st.Size(),
			"modified_at": st.ModTime().UTC().Format(time.RFC3339),
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("image-generation-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".svg", ".ico":
		return true
	default:
		return false
	}
}
