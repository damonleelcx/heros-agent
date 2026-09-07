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

// QwenKey is the credential for the Qwen provider (Alibaba Cloud Model Studio / DashScope).
//
// 🔴 The key is REGIONAL. A key issued in the Beijing account namespace is rejected by the Singapore
// host and vice versa, with a 401 indistinguishable from a revoked key. Changing region means changing
// this credential and the client's BaseURL together — see internal/provider/qwen.DefaultBaseURL.
func QwenKey() (string, error) { return RequireKey("QWEN_API_KEY") }

// QwenBaseURL is the OpenAI-compatible endpoint the Qwen client talks to. Empty means "use the
// provider package's documented default", which is Model Studio in the Beijing region.
//
// 🔴 THIS IS NOT A PERFORMANCE KNOB, AND IT IS NOT INDEPENDENT OF QwenKey. A credential is issued
// against ONE host. Model Studio Beijing, Model Studio Singapore and the token-plan MaaS host
// (token-plan.cn-beijing.maas.aliyuncs.com) each reject the others' keys with a 401 whose body is
// indistinguishable from a revoked key, so pointing this at a new host without moving QWEN_API_KEY in
// the SAME deployment change takes the service off the air with a misleading error. The two variables
// travel together, always.
//
// Why an override existed nowhere until now: the endpoint was a constant because there had only ever
// been one. When the deployment moved to a prepaid token plan on a different host, that constant was
// the only thing standing between a credential rotation and a rebuild — the sibling service already
// had OBA_QWEN_BASE_URL for exactly this reason, so this mirrors it rather than inventing a shape.
//
// Unset is deliberately the default rather than a region name that gets assembled into a URL: a
// deployment that has not stated an endpoint gets the one the client documents, and can be read
// literally instead of decoded.
func QwenBaseURL() string { return strings.TrimSpace(os.Getenv("QWEN_BASE_URL")) }
