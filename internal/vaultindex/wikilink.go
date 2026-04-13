package vaultindex

import (
	"path/filepath"
	"strings"
)

// ParseWikilinks extracts inner contents of Obsidian-style [[links]].
// Embeds ![[...]] are ignored.
func ParseWikilinks(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		idx := strings.Index(s[i:], "[[")
		if idx < 0 {
			break
		}
		pos := i + idx
		if pos > 0 && s[pos-1] == '!' {
			i = pos + 2
			continue
		}
		rest := s[pos+2:]
		end := strings.Index(rest, "]]")
		if end < 0 {
			break
		}
		inner := strings.TrimSpace(rest[:end])
		if inner != "" {
			out = append(out, inner)
		}
		i = pos + 2 + end + 2
	}
	return out
}

// linkIndex resolves wikilink targets to vault-relative paths (slash-separated).
type linkIndex struct {
	relSet    map[string]struct{}
	stemToRel map[string]string
}

func buildLinkIndex(files []fileEntry) *linkIndex {
	ix := &linkIndex{
		relSet:    map[string]struct{}{},
		stemToRel: map[string]string{},
	}
	for _, f := range files {
		rel := filepath.ToSlash(f.rel)
		ix.relSet[rel] = struct{}{}
		base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		key := strings.ToLower(strings.TrimSpace(base))
		if key == "" {
			continue
		}
		if _, ok := ix.stemToRel[key]; !ok {
			ix.stemToRel[key] = rel
		}
	}
	return ix
}

// resolve maps a single wikilink inner string to a vault rel path if the target note exists.
func (ix *linkIndex) resolve(fromRel, inner string) (dstRel, section, display string, ok bool) {
	target := strings.TrimSpace(inner)
	if target == "" {
		return "", "", "", false
	}
	display = target
	if i := strings.Index(target, "|"); i >= 0 {
		left := strings.TrimSpace(target[:i])
		right := strings.TrimSpace(target[i+1:])
		target = left
		if right != "" {
			display = right
		}
	}
	section = ""
	if i := strings.Index(target, "#"); i >= 0 {
		section = strings.TrimSpace(target[i+1:])
		target = strings.TrimSpace(target[:i])
	}
	if target == "" {
		return "", section, display, false
	}

	ts := filepath.ToSlash(strings.TrimSpace(target))
	ts = strings.TrimSuffix(ts, ".md")
	ts = strings.TrimSuffix(ts, ".MD")

	var candidates []string
	switch {
	case strings.HasPrefix(ts, "./"):
		dir := filepath.ToSlash(filepath.Dir(fromRel))
		c := filepath.ToSlash(filepath.Join(dir, strings.TrimPrefix(ts, "./")))
		candidates = append(candidates, c)
	case strings.HasPrefix(ts, "../"):
		dir := filepath.ToSlash(filepath.Dir(fromRel))
		c := filepath.ToSlash(filepath.Join(dir, ts))
		candidates = append(candidates, c)
	case strings.Contains(ts, "/"):
		candidates = append(candidates, filepath.ToSlash(filepath.Clean(ts)))
		dir := filepath.ToSlash(filepath.Dir(fromRel))
		candidates = append(candidates, filepath.ToSlash(filepath.Join(dir, ts)))
	default:
		if r, hit := ix.stemToRel[strings.ToLower(ts)]; hit {
			return r, section, display, true
		}
		return "", section, display, false
	}

	for _, c := range candidates {
		c = filepath.ToSlash(filepath.Clean(c))
		if strings.HasPrefix(c, "../") {
			continue
		}
		if _, hit := ix.relSet[c]; hit {
			return c, section, display, true
		}
		if filepath.Ext(c) == "" {
			md := c + ".md"
			if _, hit := ix.relSet[md]; hit {
				return md, section, display, true
			}
		}
	}
	return "", section, display, false
}
