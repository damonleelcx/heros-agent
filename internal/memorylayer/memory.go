package memorylayer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/memoryfs"
)

// AppendEpisodic stores a conversation turn (episodic layer). tenantID scopes rows when non-empty.
// When dataDir is set, also appends memory/<tenant>/sessions/<session>/turns.jsonl (authoritative folder log).
func AppendEpisodic(db *sql.DB, dataDir, tenantID, sessionID, role, content string, importance float64) (string, error) {
	h := sha256.Sum256([]byte(tenantID + "\n" + sessionID + "\n" + role + "\n" + content + "\n" + time.Now().String()))
	id := hex.EncodeToString(h[:16])
	memRel := ""
	if dataDir != "" {
		var err error
		memRel, err = memoryfs.AppendTurn(dataDir, tenantID, sessionID, id, role, content, importance)
		if err != nil {
			return "", err
		}
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO episodic_memory (id, tenant_id, session_id, role, content, importance, memory_session_rel) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, tenantID, sessionID, role, content, importance, memRel)
	return id, err
}

// naiveEmbedding is a deterministic stub until pgvector/Qdrant is wired; good for local demos.
func naiveEmbedding(text string) []float64 {
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

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := 0; i < len(a) && i < len(b); i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func retrieveSemanticSQLiteLegacy(db *sql.DB, tenantID, query string, k int) ([]string, error) {
	if k <= 0 {
		k = 5
	}
	q := naiveEmbedding(query)
	var rows *sql.Rows
	var err error
	if tenantID == "" {
		rows, err = db.Query(`SELECT text, embedding_json FROM semantic_chunks`)
	} else {
		rows, err = db.Query(`SELECT text, embedding_json FROM semantic_chunks WHERE tenant_id = ?`, tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		text  string
		score float64
	}
	var all []scored
	for rows.Next() {
		var text, ej string
		if err := rows.Scan(&text, &ej); err != nil {
			return nil, err
		}
		var emb []float64
		if ej != "" {
			_ = json.Unmarshal([]byte(ej), &emb)
		}
		all = append(all, scored{text: text, score: cosine(q, emb)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	var out []string
	for i := 0; i < len(all) && i < k; i++ {
		out = append(out, all[i].text)
	}
	return out, rows.Err()
}

// UpsertEntity / Link for structural knowledge graph (SQLite-backed).
func UpsertEntity(db *sql.DB, id, name, kind, propsJSON string) error {
	_, err := db.Exec(`INSERT INTO graph_entities (id, name, kind, props_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, props_json=excluded.props_json`,
		id, name, kind, propsJSON)
	return err
}

func Link(db *sql.DB, edgeID, src, dst, rel, propsJSON string) error {
	_, err := db.Exec(`INSERT INTO graph_edges (id, src_id, dst_id, rel, props_json) VALUES (?, ?, ?, ?, ?)`,
		edgeID, src, dst, rel, propsJSON)
	return err
}

// SessionOptimizer builds a focal context pack under a token budget (character proxy).
func SessionOptimizer(db *sql.DB, tenantID, sessionID string, budgetChars int) (summary string, fragments []string, err error) {
	if budgetChars <= 0 {
		budgetChars = 8000
	}
	var rows *sql.Rows
	if tenantID == "" {
		rows, err = db.Query(`SELECT role, content, importance FROM episodic_memory WHERE session_id = ? ORDER BY created_at ASC`, sessionID)
	} else {
		rows, err = db.Query(`SELECT role, content, importance FROM episodic_memory WHERE session_id = ? AND tenant_id = ? ORDER BY created_at ASC`, sessionID, tenantID)
	}
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	type turn struct {
		role, content string
		imp           float64
	}
	var turns []turn
	for rows.Next() {
		var t turn
		if err := rows.Scan(&t.role, &t.content, &t.imp); err != nil {
			return "", nil, err
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	// Focal retrieval: highest importance first, then recent
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].imp != turns[j].imp {
			return turns[i].imp > turns[j].imp
		}
		return i > j
	})
	var b strings.Builder
	used := 0
	for _, t := range turns {
		line := fmt.Sprintf("[%s] %s\n", t.role, t.content)
		if used+len(line) > budgetChars {
			break
		}
		b.WriteString(line)
		used += len(line)
		fragments = append(fragments, strings.TrimSpace(t.content))
	}
	rolling := fmt.Sprintf("Rolling focal context (%d chars, session=%s):\n%s", used, sessionID, b.String())
	return rolling, fragments, nil
}

// ApplyContextMutation applies JSON ops from Layer 2 proposals: promote_session, graph_link.
// Neo4j receives the same structural writes as SQLite when neo is non-nil (enterprise graph).
func ApplyContextMutation(ctx context.Context, db *sql.DB, neo *neo4jstore.Store, inf *VectorInfra, tenantID string, diff string) (rollback string, err error) {
	var ops struct {
		Promote []struct {
			SessionID string  `json:"session_id"`
			Threshold float64 `json:"threshold"`
		} `json:"promote"`
		Links []struct {
			EdgeID   string `json:"edge_id"`
			Src      string `json:"src"`
			Dst      string `json:"dst"`
			Rel      string `json:"rel"`
			Props    any    `json:"props"`
			EntityID string `json:"entity_id"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(diff), &ops); err != nil {
		return "", fmt.Errorf("context layer diff must be JSON: %w", err)
	}
	for _, p := range ops.Promote {
		if _, err := RunConsolidation(ctx, db, inf, tenantID, p.SessionID, p.Threshold); err != nil {
			return "", err
		}
	}
	for _, l := range ops.Links {
		if l.EntityID != "" {
			pj, _ := json.Marshal(l.Props)
			if err := UpsertEntity(db, l.EntityID, l.Name, l.Kind, string(pj)); err != nil {
				return "", err
			}
			if neo != nil {
				if err := neo.UpsertEntity(ctx, l.EntityID, l.Name, l.Kind, string(pj)); err != nil {
					return "", err
				}
			}
		}
		if l.EdgeID != "" && l.Src != "" && l.Dst != "" {
			pj, _ := json.Marshal(l.Props)
			if err := Link(db, l.EdgeID, l.Src, l.Dst, l.Rel, string(pj)); err != nil {
				return "", err
			}
			if neo != nil {
				if err := neo.Link(ctx, l.EdgeID, l.Src, l.Dst, l.Rel, string(pj)); err != nil {
					return "", err
				}
			}
		}
	}
	return "context:apply:" + diff, nil
}
