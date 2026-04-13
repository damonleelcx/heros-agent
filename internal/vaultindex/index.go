package vaultindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/uuid"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/embeddings"
	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/infra/qdrant"
	"github.com/heros-foreal/agentd/internal/memorylayer"
)

// Result summarizes one indexing pass for a vault root.
type Result struct {
	FilesSeen      int
	FilesIndexed   int
	FilesSkipped   int
	ChunksWritten  int
	OrphansRemoved int
}

// IndexVault scans one configured vault (read-only on disk), updates semantic_chunks, optional Qdrant,
// and wikilinks in graph_entities / graph_edges (+ Neo4j when neo is non-nil).
func IndexVault(ctx context.Context, db *sql.DB, inf *memorylayer.VectorInfra, neo *neo4jstore.Store, v config.KnowledgeVault) (Result, error) {
	var r Result
	root := strings.TrimSpace(v.Path)
	if root == "" {
		return r, fmt.Errorf("knowledge_vaults: empty path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return r, err
	}
	st, err := os.Stat(absRoot)
	if err != nil {
		return r, fmt.Errorf("vault path %q: %w", absRoot, err)
	}
	if !st.IsDir() {
		return r, fmt.Errorf("vault path is not a directory: %s", absRoot)
	}
	tenantID := strings.TrimSpace(v.TenantID)

	includes := v.IncludeGlobs
	if len(includes) == 0 {
		includes = []string{"**/*.md", "**/*.mdc"}
	}
	excludes := append([]string{}, v.ExcludeGlobs...)
	defaultExcludes := []string{".obsidian/**", "**/.git/**", "**/node_modules/**"}
	excludes = append(excludes, defaultExcludes...)

	stateRows, err := loadVaultState(db, absRoot, tenantID)
	if err != nil {
		return r, err
	}
	var files []fileEntry
	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".obsidian" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !v.FollowSymlinks {
			if fi, err := d.Info(); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				return nil
			}
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !matchAny(includes, rel, false) {
			return nil
		}
		if matchAny(excludes, rel, true) {
			return nil
		}
		files = append(files, fileEntry{abs: path, rel: rel})
		return nil
	})
	if walkErr != nil {
		return r, walkErr
	}

	linkIdx := buildLinkIndex(files)

	seenRel := map[string]struct{}{}
	for _, fe := range files {
		seenRel[fe.rel] = struct{}{}
	}

	// Remove DB/Qdrant for deleted files
	for fk, oldRel := range stateRows {
		if _, ok := seenRel[oldRel]; ok {
			continue
		}
		if err := deleteFileChunks(ctx, db, inf, tenantID, fk); err != nil {
			return r, err
		}
		if err := removeVaultNoteFromGraph(ctx, db, neo, tenantID, absRoot, oldRel); err != nil {
			return r, err
		}
		_, _ = db.Exec(`DELETE FROM vault_index_state WHERE vault_root = ? AND tenant_id = ? AND rel_path = ?`,
			absRoot, tenantID, oldRel)
		r.OrphansRemoved++
	}

	for _, fe := range files {
		r.FilesSeen++
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		default:
		}
		fk := FileKey(tenantID, absRoot, fe.rel)

		b, err := os.ReadFile(fe.abs)
		if err != nil {
			log.Printf("vaultindex: skip read %s: %v", fe.abs, err)
			continue
		}
		sum := sha256.Sum256(b)
		hexSum := hex.EncodeToString(sum[:])
		fi, err := os.Stat(fe.abs)
		if err != nil {
			continue
		}
		mtime := fi.ModTime().Unix()

		var prevHash sql.NullString
		err = db.QueryRow(`SELECT content_sha256 FROM vault_index_state WHERE vault_root = ? AND tenant_id = ? AND rel_path = ?`,
			absRoot, tenantID, fe.rel).Scan(&prevHash)
		if err == nil && prevHash.Valid && prevHash.String == hexSum {
			r.FilesSkipped++
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return r, err
		}

		chunks := ChunkMarkdown(string(b))
		if len(chunks) == 0 {
			// Empty note: remove old chunks if any
			if err := deleteFileChunks(ctx, db, inf, tenantID, fk); err != nil {
				return r, err
			}
			if err := syncWikilinks(ctx, db, neo, tenantID, absRoot, fe.rel, string(b), linkIdx); err != nil {
				return r, err
			}
			_, _ = db.Exec(`INSERT OR REPLACE INTO vault_index_state (vault_root, rel_path, tenant_id, file_key, content_sha256, mtime_unix, chunk_count, indexed_at)
				VALUES (?, ?, ?, ?, ?, ?, 0, datetime('now'))`,
				absRoot, fe.rel, tenantID, fk, hexSum, mtime)
			r.FilesIndexed++
			continue
		}

		if err := deleteFileChunks(ctx, db, inf, tenantID, fk); err != nil {
			return r, err
		}

		texts := make([]string, len(chunks))
		for i := range chunks {
			texts[i] = chunks[i].Text
		}
		var embedder embeddings.Embedder
		if inf != nil && inf.Emb != nil {
			embedder = inf.Emb
		}
		var vecs [][]float64
		if embedder != nil {
			vecs, err = embedder.Embed(ctx, texts)
			if err != nil {
				return r, fmt.Errorf("embed vault %s: %w", fe.rel, err)
			}
		} else {
			for _, t := range texts {
				vecs = append(vecs, naiveEmbeddingForVault(t))
			}
		}

		srcPrefix := vaultSourcePrefix(fe.rel)
		for i, ch := range chunks {
			if i >= len(vecs) {
				break
			}
			cid := ChunkSQLiteID(fk, ch.Index)
			source := fmt.Sprintf("%s#c%d", srcPrefix, ch.Index)
			body := ch.Text
			if strings.TrimSpace(ch.Heading) != "" {
				body = "## " + ch.Heading + "\n\n" + ch.Text
			}
			embJSON, _ := json.Marshal(vecs[i])
			_, err = db.Exec(`INSERT OR REPLACE INTO semantic_chunks (id, tenant_id, source, text, embedding_json) VALUES (?, ?, ?, ?, ?)`,
				cid, tenantID, source, body, string(embJSON))
			if err != nil {
				return r, err
			}
			r.ChunksWritten++

			if inf != nil && inf.Qdrant != nil && inf.Emb != nil {
				pid := qdrantPointID(cid)
				payload := map[string]any{
					"text":          body,
					"tenant_id":     tenantID,
					"source_kind":   "vault",
					"vault_root":    filepath.ToSlash(absRoot),
					"vault_rel_path": filepath.ToSlash(fe.rel),
					"vault_file_key": fk,
					"chunk_index":   ch.Index,
					"heading":       ch.Heading,
					"mtime_unix":    mtime,
					"node_id":       inf.NodeID,
				}
				if err := inf.Qdrant.UpsertPoints(ctx, inf.Collection, []qdrant.Point{
					{ID: pid, Vector: vecs[i], Payload: payload},
				}); err != nil {
					return r, err
				}
			}
		}

		if err := syncWikilinks(ctx, db, neo, tenantID, absRoot, fe.rel, string(b), linkIdx); err != nil {
			return r, err
		}

		_, err = db.Exec(`INSERT OR REPLACE INTO vault_index_state (vault_root, rel_path, tenant_id, file_key, content_sha256, mtime_unix, chunk_count, indexed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			absRoot, fe.rel, tenantID, fk, hexSum, mtime, len(chunks))
		if err != nil {
			return r, err
		}
		r.FilesIndexed++
	}

	return r, nil
}

type fileEntry struct {
	abs, rel string
}

func loadVaultState(db *sql.DB, absRoot, tenantID string) (map[string]string, error) {
	rows, err := db.Query(`SELECT file_key, rel_path FROM vault_index_state WHERE vault_root = ? AND tenant_id = ?`, absRoot, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var fk, rp string
		if err := rows.Scan(&fk, &rp); err != nil {
			return nil, err
		}
		out[fk] = rp
	}
	return out, rows.Err()
}

func deleteFileChunks(ctx context.Context, db *sql.DB, inf *memorylayer.VectorInfra, tenantID, fileKey string) error {
	pat := "vk-" + fileKey + "-*"
	if _, err := db.Exec(`DELETE FROM semantic_chunks WHERE tenant_id = ? AND id GLOB ?`, tenantID, pat); err != nil {
		return err
	}
	if inf != nil && inf.Qdrant != nil {
		if err := inf.Qdrant.DeletePointsByFilter(ctx, inf.Collection, vaultFileFilter(tenantID, fileKey)); err != nil {
			return err
		}
	}
	return nil
}

func vaultFileFilter(tenantID, fileKey string) map[string]any {
	must := []map[string]any{
		{"key": "source_kind", "match": map[string]any{"value": "vault"}},
		{"key": "vault_file_key", "match": map[string]any{"value": fileKey}},
	}
	if tenantID != "" {
		must = append(must, map[string]any{"key": "tenant_id", "match": map[string]any{"value": tenantID}})
	}
	return map[string]any{"must": must}
}

func qdrantPointID(sqliteChunkID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("heros-vault:"+sqliteChunkID)).String()
}

func vaultSourcePrefix(relPath string) string {
	return "vault:" + filepath.ToSlash(relPath)
}

func matchAny(patterns []string, rel string, isExclude bool) bool {
	slash := filepath.ToSlash(rel)
	for _, p := range patterns {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		ok, err := doublestar.Match(p, slash)
		if err != nil {
			if isExclude {
				continue
			}
			return false
		}
		if ok {
			return true
		}
	}
	return false
}

func naiveEmbeddingForVault(text string) []float64 {
	// Match memorylayer naiveEmbedding dim for scoring consistency with mixed chunks.
	const dim = 64
	v := make([]float64, dim)
	t := strings.ToLower(text)
	for i, r := range t {
		if i >= 512 {
			break
		}
		v[int(r)%dim] += 1
	}
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return v
	}
	inv := 1 / math.Sqrt(sum)
	for i := range v {
		v[i] *= inv
	}
	return v
}
