package platform

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/embeddings"
	"github.com/heros-foreal/agentd/internal/infra/natsbus"
	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/infra/qdrant"
	"github.com/heros-foreal/agentd/internal/memorylayer"
)

// Runtime holds enterprise dependencies (vector / graph / bus) plus the authoritative SQLite ledger.
type Runtime struct {
	DB     *sql.DB
	Cfg    config.Config
	Qdrant *qdrant.Client
	Neo    *neo4jstore.Store
	Bus    *natsbus.Bus
	Coll   string
	Emb    embeddings.Embedder
}

// VectorInfra bundles vector + messaging for memorylayer.
func (r *Runtime) VectorInfra() *memorylayer.VectorInfra {
	if r == nil {
		return nil
	}
	return &memorylayer.VectorInfra{
		Qdrant:     r.Qdrant,
		Collection: r.Coll,
		Emb:        r.Emb,
		Bus:        r.Bus,
		NodeID:     r.Cfg.NodeID,
	}
}

// Bootstrap connects optional Qdrant, Neo4j, NATS and ensures Qdrant collection dimension matches the embedder.
func Bootstrap(ctx context.Context, cfg config.Config, database *sql.DB) (*Runtime, error) {
	rt := &Runtime{DB: database, Cfg: cfg, Coll: cfg.QdrantCollection}
	if rt.Coll == "" {
		rt.Coll = "heros_memory"
	}

	edims := cfg.EmbeddingDims
	if edims <= 0 {
		if cfg.OpenAIAPIKey != "" && cfg.EmbeddingModel != "" {
			edims = 256
		} else {
			edims = 128
		}
	}
	rt.Emb = embeddings.NewFromConfig(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.EmbeddingModel, edims)
	effDims := rt.Emb.Dims()

	if cfg.QdrantURL != "" {
		qc := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey)
		if err := qc.EnsureCollection(ctx, rt.Coll, uint64(effDims)); err != nil {
			return nil, fmt.Errorf("qdrant: %w", err)
		}
		if err := qc.Health(ctx); err != nil {
			return nil, fmt.Errorf("qdrant health: %w", err)
		}
		rt.Qdrant = qc
	}

	if cfg.Neo4jURI != "" && cfg.Neo4jUser != "" {
		st, err := neo4jstore.Connect(ctx, cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword, cfg.Neo4jDatabase)
		if err != nil {
			return nil, fmt.Errorf("neo4j: %w", err)
		}
		rt.Neo = st
	}

	if cfg.NatsURL != "" {
		b, err := natsbus.Connect(cfg.NatsURL, cfg.NodeID)
		if err != nil {
			return nil, fmt.Errorf("nats: %w", err)
		}
		rt.Bus = b
		if cfg.JetStreamEnabled {
			maxAge := time.Duration(cfg.JetStreamMaxAgeHours) * time.Hour
			if err := b.InitJetStream(cfg.JetStreamStreamName, maxAge); err != nil {
				return nil, fmt.Errorf("jetstream: %w", err)
			}
		}
	}

	return rt, nil
}

func (r *Runtime) Close(ctx context.Context) {
	if r == nil {
		return
	}
	if r.Neo != nil {
		_ = r.Neo.Close(ctx)
	}
	if r.Bus != nil {
		r.Bus.Close()
	}
}
