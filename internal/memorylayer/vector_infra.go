package memorylayer

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/heros-foreal/agentd/internal/embeddings"
	"github.com/heros-foreal/agentd/internal/infra/natsbus"
	"github.com/heros-foreal/agentd/internal/infra/qdrant"
)

// VectorInfra wires Qdrant + embedder + optional NATS for semantic memory (enterprise path).
type VectorInfra struct {
	Qdrant     *qdrant.Client
	Collection string
	Emb        embeddings.Embedder
	Bus        *natsbus.Bus
	NodeID     string
}

// RunConsolidation promotes high-importance episodic turns into semantic storage.
// When inf is non-nil and Qdrant is configured, vectors are indexed in Qdrant; SQLite always stores a row for audit/offline.
func RunConsolidation(ctx context.Context, db *sql.DB, inf *VectorInfra, tenantID, sessionID string, threshold float64) (promoted int, err error) {
	var rows *sql.Rows
	if tenantID == "" {
		rows, err = db.Query(`SELECT id, content, importance FROM episodic_memory WHERE session_id = ?`, sessionID)
	} else {
		rows, err = db.Query(`SELECT id, content, importance FROM episodic_memory WHERE session_id = ? AND tenant_id = ?`, sessionID, tenantID)
	}
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, content string
		var imp float64
		if err := rows.Scan(&id, &content, &imp); err != nil {
			return promoted, err
		}
		if imp < threshold {
			continue
		}
		chunkID := "sem-" + id
		var vec []float64
		if inf != nil && inf.Qdrant != nil && inf.Emb != nil {
			vecs, e := inf.Emb.Embed(ctx, []string{content})
			if e != nil {
				return promoted, e
			}
			if len(vecs) > 0 {
				vec = vecs[0]
			}
			pointID := uuid.NewString()
			payload := map[string]any{
				"text":        content,
				"session_id":  sessionID,
				"episodic_id": id,
				"node_id":     inf.NodeID,
				"tenant_id":   tenantID,
				"importance":  imp,
			}
			if err := inf.Qdrant.UpsertPoints(ctx, inf.Collection, []qdrant.Point{
				{ID: pointID, Vector: vec, Payload: payload},
			}); err != nil {
				return promoted, err
			}
			if inf.Bus != nil {
				_ = inf.Bus.PublishMemoryPromoted(sessionID, pointID, content, imp)
			}
		} else {
			vec = naiveEmbedding(content)
		}
		b, _ := json.Marshal(vec)
		_, err = db.Exec(`INSERT OR REPLACE INTO semantic_chunks (id, tenant_id, source, text, embedding_json) VALUES (?, ?, ?, ?, ?)`,
			chunkID, tenantID, "episodic:"+sessionID, content, string(b))
		if err != nil {
			return promoted, err
		}
		promoted++
	}
	return promoted, rows.Err()
}

// RetrieveSemantic returns top-k chunks; prefers Qdrant when configured, else SQLite cosine.
func RetrieveSemantic(ctx context.Context, db *sql.DB, inf *VectorInfra, tenantID, query string, k int) ([]string, error) {
	if k <= 0 {
		k = 5
	}
	if inf != nil && inf.Qdrant != nil && inf.Emb != nil {
		vecs, err := inf.Emb.Embed(ctx, []string{query})
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			return nil, nil
		}
		hits, err := inf.Qdrant.Search(ctx, inf.Collection, vecs[0], k*3, true)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, h := range hits {
			if tenantID != "" && h.Payload != nil {
				if tid, ok := h.Payload["tenant_id"].(string); ok && tid != tenantID {
					continue
				}
			}
			if h.Text != "" {
				out = append(out, h.Text)
			} else if h.Payload != nil {
				if t, ok := h.Payload["text"].(string); ok {
					out = append(out, t)
				}
			}
			if len(out) >= k {
				break
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return retrieveSemanticSQLiteLegacy(db, tenantID, query, k)
}
