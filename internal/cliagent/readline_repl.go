package cliagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/heros-foreal/agentd/internal/config"
)

// RunReadlineREPL is a line-editing REPL (history in ~/.heros-cli.history).
func RunReadlineREPL(ctx context.Context, s *Session, out, errOut io.Writer) error {
	if err := s.PrimeSystem(ctx); err != nil {
		return fmt.Errorf("prime system: %w", err)
	}
	home, _ := os.UserHomeDir()
	hist := filepath.Join(home, ".heros-cli.history")
	rl, err := readline.NewEx(&readline.Config{
		Prompt:       "> ",
		HistoryFile:  hist,
		HistoryLimit: 800,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rl.Close() }()

	_, _ = fmt.Fprintf(out, "heros — session=%s  workdir=%s  (streaming=%v  /exit to quit)\n"+
		"  Tip: /pending | approve N | approve all | /approve <id>  (/help)\n",
		s.SessionID, s.WorkDir, s.Stream)

	for {
		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			if errors.Is(err, io.EOF) {
				_ = config.SaveCLIWorkdir(s.WorkDir)
				_, _ = fmt.Fprintln(out, "bye")
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if s.TryBulkApproveCommand(ctx, line, out, errOut) {
			continue
		}
		if s.TryApprovalNumberCommand(ctx, line, out, errOut) {
			continue
		}
		if c, q := s.DispatchReplSlash(ctx, line, out, errOut); c {
			if q {
				return nil
			}
			continue
		}
		if err := s.RunUserTurn(ctx, line, out); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
}
