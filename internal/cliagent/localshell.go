package cliagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// RunLocalShell runs a command on the CLI host with working directory workDir.
func RunLocalShell(ctx context.Context, workDir, command string) (output string, exitErr error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("empty command")
	}
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", command)
	}
	c.Dir = workDir
	b, err := c.CombinedOutput()
	out := strings.TrimSpace(string(b))
	return out, err
}

// LocalShellResult is JSON returned to the model for heros_shell.
func LocalShellResult(output string, err error) string {
	var errS string
	if err != nil {
		errS = err.Error()
	}
	b, _ := json.Marshal(map[string]string{"output": output, "error": errS, "host": "cli_local"})
	return string(b)
}
