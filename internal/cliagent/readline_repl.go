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

	_, _ = fmt.Fprintf(out, "heros-cli — session=%s  workdir=%s  (streaming=%v  /exit to quit)\n",
		s.SessionID, s.WorkDir, s.Stream)

	for {
		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			if errors.Is(err, io.EOF) {
				_, _ = fmt.Fprintln(out, "bye")
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case line == "/exit", line == "/quit":
			_, _ = fmt.Fprintln(out, "bye")
			return nil
		case line == "/help":
			printHelp(out)
			continue
		case line == "/refresh":
			if err := s.RefreshContext(ctx); err != nil {
				_, _ = fmt.Fprintf(errOut, "refresh: %v\n", err)
			} else {
				_, _ = fmt.Fprintln(out, "(catalog block appended to context)")
			}
			continue
		case strings.HasPrefix(line, "/"):
			_, _ = fmt.Fprintf(errOut, "unknown command %q (try /help)\n", line)
			continue
		}
		if err := s.RunUserTurn(ctx, line, out); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
}
