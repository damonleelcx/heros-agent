//go:build pgproof

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// hasher accumulates a tree hash: path and length are mixed in alongside the bytes, so a file
// renamed or truncated changes the sum even if some other file absorbs its content.
type hasher struct{ h hash.Hash }

func newHasher() *hasher { return &hasher{h: sha256.New()} }

func (x *hasher) add(rel string, b []byte) {
	_, _ = fmt.Fprintf(x.h, "%s\x00%d\x00", rel, len(b))
	_, _ = x.h.Write(b)
}

func (x *hasher) sum() string { return hex.EncodeToString(x.h.Sum(nil)) }

func sortStrings(s []string) { sort.Strings(s) }
