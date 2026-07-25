package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// git.go derives repo identity (url + commit) by reading .git files ONLY — never a `git` subprocess and
// never the network (invariant I1, and the offline guarantee). It is the same logic cmd/discover uses,
// kept identical so the two surfaces resolve the same identity for the same repo.

var hex7to64 = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// gitCommit resolves HEAD to a commit sha by reading .git files only.
func gitCommit(repo string) string {
	head, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(s, "ref: "); ok {
		if b, err := os.ReadFile(filepath.Join(repo, ".git", filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
		if sha := packedRef(repo, ref); sha != "" {
			return sha
		}
		return ""
	}
	return s // detached HEAD: the sha itself
}

func packedRef(repo, ref string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			return fields[0]
		}
	}
	return ""
}

// gitRemoteURL reads the origin remote url from .git/config (plain read — no `git` subprocess).
func gitRemoteURL(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = t == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if u, ok := strings.CutPrefix(t, "url = "); ok {
				return strings.TrimSpace(u)
			}
			if u, ok := strings.CutPrefix(t, "url="); ok {
				return strings.TrimSpace(u)
			}
		}
	}
	return ""
}
