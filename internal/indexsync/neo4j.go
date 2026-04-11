package indexsync

import (
	"context"
	"database/sql"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/skillindex"
	"github.com/heros-foreal/agentd/internal/toolindex"
)

// SyncSkillsTools mirrors filesystem skill/tool index into Neo4j (graph view of the same layout).
func SyncSkillsTools(ctx context.Context, store *neo4jstore.Store, db *sql.DB) error {
	if store == nil {
		return nil
	}
	skills, err := skillindex.List(db)
	if err != nil {
		return err
	}
	tools, err := toolindex.List(db)
	if err != nil {
		return err
	}
	for _, s := range skills {
		sid := skillindex.ScopedID(s.TenantScope, s.Name)
		_, err := neo4j.ExecuteQuery(ctx, store.Driver,
			`MERGE (x:HerosSkill {id: $id}) SET x.rel_path = $rp, x.title = $t, x.tenant_scope = $ts, x.name = $nm`,
			map[string]any{"id": sid, "rp": s.RelPath, "t": s.Title, "ts": s.TenantScope, "nm": s.Name},
			neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(store.Database))
		if err != nil {
			return err
		}
	}
	for _, t := range tools {
		tid := toolindex.ScopedToolID(t.TenantScope, t.ToolID)
		_, err := neo4j.ExecuteQuery(ctx, store.Driver,
			`MERGE (y:HerosTool {id: $id}) SET y.rel_path = $rp, y.risk_tier = $r, y.tenant_scope = $ts, y.name = $nm`,
			map[string]any{"id": tid, "rp": t.RelPath, "r": t.RiskTier, "ts": t.TenantScope, "nm": t.ToolID},
			neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(store.Database))
		if err != nil {
			return err
		}
	}
	for _, s := range skills {
		fromID := skillindex.ScopedID(s.TenantScope, s.Name)
		for _, d := range s.DependsOn {
			if d == "" {
				continue
			}
			toID := skillindex.ResolveDependsTarget(skills, s.TenantScope, d)
			if toID == "" {
				continue
			}
			_, err := neo4j.ExecuteQuery(ctx, store.Driver,
				`MATCH (a:HerosSkill {id: $from}) MERGE (b:HerosSkill {id: $to}) MERGE (a)-[:DEPENDS_ON]->(b)`,
				map[string]any{"from": fromID, "to": toID},
				neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(store.Database))
			if err != nil {
				return err
			}
		}
		for _, raw := range s.Tools {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			tlID := toolindex.ResolveToolTarget(tools, s.TenantScope, raw)
			if tlID == "" {
				continue
			}
			_, err := neo4j.ExecuteQuery(ctx, store.Driver,
				`MATCH (sk:HerosSkill {id: $sk}) MERGE (tl:HerosTool {id: $tl}) MERGE (sk)-[:USES_TOOL]->(tl)`,
				map[string]any{"sk": fromID, "tl": tlID},
				neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(store.Database))
			if err != nil {
				return err
			}
		}
	}
	return nil
}
