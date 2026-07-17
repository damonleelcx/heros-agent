//go:build pgproof

package submit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// indexNodes maps a fixture call site's enclosing function name -> its node_id, so a test can name a
// node by what it IS ("classify") rather than by an opaque hash.
//
// It goes through discovery.IndexGoCallSites — the same indexer the transform engine uses — so the
// ids here are the ids a Variant Spec must carry. Deriving them any other way would let a test pass
// against node_ids the product would never produce.
func indexNodes(root string) (map[string]string, error) {
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	out := map[string]string{}
	for id, s := range sites {
		fn := enclosingFunc(lines, s.LineStart)
		if fn == "" {
			return nil, fmt.Errorf("call site %s at line %d has no enclosing func", id, s.LineStart)
		}
		out[fn] = id
	}
	return out, nil
}

// enclosingFunc walks back from a line to the `func` that contains it.
func enclosingFunc(lines []string, line int) string {
	for i := line - 1; i >= 0 && i < len(lines); i-- {
		if strings.HasPrefix(lines[i], "func ") {
			n := strings.TrimPrefix(lines[i], "func ")
			if p := strings.IndexByte(n, '('); p > 0 {
				return n[:p]
			}
		}
	}
	return ""
}

// hashTree hashes every file under dir except .git, whose bytes legitimately churn.
//
// This is how "submit never touches the user's tree" is checked: a byte-level fingerprint before and
// after. Anything weaker — a timestamp, a `git status` — would miss a write-then-restore.
func hashTree(t interface{ Fatalf(string, ...any) }, dir string) string {
	h := sha256.New()
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		rel, _ := filepath.Rel(dir, f)
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
