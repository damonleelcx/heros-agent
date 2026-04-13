package installpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GoBinDir returns the directory where `go install` places binaries (GOBIN or GOPATH/bin).
func GoBinDir() (string, error) {
	gobin, err := goEnv("GOBIN")
	if err != nil {
		return "", err
	}
	gobin = strings.TrimSpace(gobin)
	if gobin != "" {
		return filepath.Abs(filepath.Clean(gobin))
	}
	gp, err := goEnv("GOPATH")
	if err != nil {
		return "", err
	}
	gp = strings.TrimSpace(gp)
	if gp == "" {
		return "", fmt.Errorf("GOPATH is empty (set GOPATH or GOBIN)")
	}
	return filepath.Abs(filepath.Clean(filepath.Join(gp, "bin")))
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w: %s", key, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// AddPathTargetDir returns the directory to add for `heros -add-path`:
// if this binary lives in Go's install bin (GOBIN or GOPATH/bin), that dir is used;
// otherwise the directory containing this executable (portable .exe / AppImage extract) is used.
// If Go is not installed, only the executable directory is available.
func AddPathTargetDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir, err := filepath.Abs(filepath.Clean(filepath.Dir(exe)))
	if err != nil {
		return "", err
	}
	gobin, err := GoBinDir()
	if err != nil {
		return exeDir, nil
	}
	gobin, err = filepath.Abs(filepath.Clean(gobin))
	if err != nil {
		return exeDir, nil
	}
	if strings.EqualFold(exeDir, gobin) {
		return gobin, nil
	}
	return exeDir, nil
}
