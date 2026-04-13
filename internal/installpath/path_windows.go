//go:build windows

package installpath

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// AddUserPATH appends dir to the current user's PATH if missing (persists for new terminals).
func AddUserPATH(dir string) error {
	dir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.READ|registry.WRITE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()

	pathVal, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("read Path: %w", err)
	}
	parts := strings.Split(pathVal, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		a, e1 := filepath.Abs(filepath.Clean(p))
		b := dir
		if e1 == nil {
			if strings.EqualFold(a, b) {
				return nil
			}
		} else if strings.EqualFold(p, dir) {
			return nil
		}
	}
	newPath := strings.TrimSuffix(pathVal, ";")
	if newPath != "" {
		newPath += ";"
	}
	newPath += dir
	if err := k.SetStringValue("Path", newPath); err != nil {
		return fmt.Errorf("write Path: %w", err)
	}
	return nil
}
