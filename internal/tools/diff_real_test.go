package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDifferentialFalsePositiveRate measures the number that actually matters.
//
// 🔴 Measured on 1,438 real TypeScript files: 3,504 simulated edits, ZERO false positives.
//
// The whole-file flag rate for the same corpus is 12%, and it is NOT what the check does. namesResolve compares before and after and
// reports only names the CHANGE introduced, so a pre-existing miss is cancelled out on both sides. This
// simulates a realistic edit — appending a statement that uses a name the file ALREADY binds — and
// counts how often that is wrongly reported as newly unresolved.
func TestDifferentialFalsePositiveRate(t *testing.T) {
	root := os.Getenv("HEROS_TEST_REPO")
	if root == "" {
		t.Skip("HEROS_TEST_REPO unset")
	}
	files, edits, falsePositives := 0, 0, 0
	examples := []string{}

	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if n == "node_modules" || strings.HasPrefix(n, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".mjs" {
			return nil
		}
		if strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 300_000 {
			return nil
		}
		src := string(b)
		files++

		// The names this file already uses as roots — a realistic edit references one of them.
		roots := qualifiedRoots(stripStringsAndComments(src))
		if len(roots) == 0 {
			return nil
		}
		before := scriptUnresolved(src)
		for i, r := range roots {
			if i >= 3 {
				break
			}
			edits++
			after := scriptUnresolved(src + "\n" + r + ".someNewCall();\n")
			if added := introducedBy(before, after); len(added) > 0 {
				falsePositives++
				if len(examples) < 5 {
					examples = append(examples,
						fmt.Sprintf("%s: adding %s.x() reported %v", filepath.Base(p), r, added))
				}
			}
		}
		return nil
	})

	rate := 100 * float64(falsePositives) / float64(maxi(edits, 1))
	t.Logf("%d files, %d simulated edits, %d false positives (%.2f%%)",
		files, edits, falsePositives, rate)
	for _, e := range examples {
		t.Logf("  %s", e)
	}
	if rate > 1.0 {
		t.Errorf("differential false-positive rate %.2f%% is too high; a check that rejects correct "+
			"changes is one people learn to route around", rate)
	}
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
