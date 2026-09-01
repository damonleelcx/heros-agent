// Package config loads runtime configuration, chiefly provider credentials.
//
// # 🔴 Why a key is only ever read from the environment or an operator-created file
//
// Never from a constant, never from a committed file, never from a flag. A key in git history is a key
// that has leaked, and the only remedy is rotation — which somebody has to notice is necessary first.
// The loader below reads `.env.local`, which .gitignore excludes, and a real environment variable wins
// over it so a deployment never depends on a file being present.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrMissingKey means a required credential was not found anywhere.
var ErrMissingKey = errors.New("config: required credential is not set")

// LoadDotEnv reads KEY=VALUE lines into the process environment WITHOUT overwriting anything already
// set. Precedence is deliberate: a real environment variable beats a file, so a container's injected
// secret is never shadowed by a developer's leftover file inside the image.
//
// A missing file is not an error — it is the normal case in production.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("config: opening %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("config: %s line %d is not KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, already := os.LookupEnv(key); already {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("config: setting %s: %w", key, err)
		}
	}
	return sc.Err()
}

// RequireKey returns a credential or an error that says how to supply it.
//
// 🔴 The error names the variable and the file but NEVER any part of the value, not even a prefix. A
// truncated key in a log is still a key fragment in a log, and the habit of printing "just the first
// six characters" is how the other twenty-nine end up beside them in the next debugging session.
func RequireKey(name string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: %s — set it in the environment, or put %s=… in .env.local "+
		"(which is git-ignored)", ErrMissingKey, name, name)
}

// DeepSeekKey is the credential for the DeepSeek provider.
func DeepSeekKey() (string, error) { return RequireKey("DEEPSEEK_API_KEY") }
