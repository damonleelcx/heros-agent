package vaultindex

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/memorylayer"
	"github.com/heros-foreal/agentd/internal/platform"
)

// RunPoller periodically reindexes configured vaults (poll_seconds > 0 per entry).
func RunPoller(ctx context.Context, db *sql.DB, rt *platform.Runtime, cfg config.Config) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var mu sync.Mutex
	last := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			var inf *memorylayer.VectorInfra
			if rt != nil {
				inf = rt.VectorInfra()
			}
			for _, v := range cfg.KnowledgeVaults {
				if v.PollSeconds <= 0 {
					continue
				}
				key := v.Path + "\x00" + v.TenantID
				mu.Lock()
				prev := last[key]
				if !prev.IsZero() && time.Since(prev) < time.Duration(v.PollSeconds)*time.Second {
					mu.Unlock()
					continue
				}
				last[key] = time.Now()
				mu.Unlock()
				cctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
				var neo *neo4jstore.Store
				if rt != nil {
					neo = rt.Neo
				}
				res, err := IndexVault(cctx, db, inf, neo, v)
				cancel()
				if err != nil {
					log.Printf("vault poll %q: %v", v.Path, err)
					continue
				}
				if res.FilesIndexed > 0 || res.OrphansRemoved > 0 {
					log.Printf("vault poll %q: indexed=%d skipped=%d chunks=%d orphans=%d",
						v.Path, res.FilesIndexed, res.FilesSkipped, res.ChunksWritten, res.OrphansRemoved)
				}
			}
		}
	}
}
