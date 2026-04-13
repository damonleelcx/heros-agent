package neo4jstore

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Store wraps a Neo4j driver (enterprise graph tier).
type Store struct {
	Driver   neo4j.DriverWithContext
	Database string
}

func Connect(ctx context.Context, uri, user, password, database string) (*Store, error) {
	if database == "" {
		database = "neo4j"
	}
	d, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, err
	}
	if err := d.VerifyAuthentication(ctx, nil); err != nil {
		_ = d.Close(ctx)
		return nil, err
	}
	s := &Store{Driver: d, Database: database}
	if err := s.ensureSchema(ctx); err != nil {
		_ = d.Close(ctx)
		return nil, err
	}
	return s, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.Driver.Close(ctx)
}

func (s *Store) ensureSchema(ctx context.Context) error {
	cq := `CREATE CONSTRAINT entity_id_unique IF NOT EXISTS FOR (e:Entity) REQUIRE e.id IS UNIQUE`
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, cq, nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	if err != nil {
		return fmt.Errorf("neo4j schema: %w", err)
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, `RETURN 1 AS ok`, nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	return err
}

// UpsertEntity MERGEs (:Entity {id}) with properties.
func (s *Store) UpsertEntity(ctx context.Context, id, name, kind, propsJSON string) error {
	cypher := `
MERGE (e:Entity {id: $id})
SET e.name = $name, e.kind = $kind, e.props_json = coalesce($props_json, "")`
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, cypher, map[string]any{
		"id":         id,
		"name":       name,
		"kind":       kind,
		"props_json": propsJSON,
	}, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	return err
}

// Link creates (src)-[:LINK {edge_id, name: rel}]->(dst).
// DeleteOutgoingLinksNamed removes relationships from src whose LINK.name matches relName (e.g. WIKILINK).
func (s *Store) DeleteOutgoingLinksNamed(ctx context.Context, srcID, relName string) error {
	cypher := `MATCH (a:Entity {id: $src})-[r:LINK]->() WHERE r.name = $rel DELETE r`
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, cypher, map[string]any{
		"src": srcID,
		"rel": relName,
	}, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	return err
}

// DeleteEntityDetach removes a node and all its relationships.
func (s *Store) DeleteEntityDetach(ctx context.Context, id string) error {
	cypher := `MATCH (e:Entity {id: $id}) DETACH DELETE e`
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, cypher, map[string]any{"id": id}, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	return err
}

func (s *Store) Link(ctx context.Context, edgeID, src, dst, rel, propsJSON string) error {
	cypher := `
MATCH (a:Entity {id: $src}), (b:Entity {id: $dst})
MERGE (a)-[r:LINK {edge_id: $edge_id}]->(b)
SET r.name = $rel, r.props_json = coalesce($props_json, "")`
	_, err := neo4j.ExecuteQuery(ctx, s.Driver, cypher, map[string]any{
		"src":        src,
		"dst":        dst,
		"edge_id":    edgeID,
		"rel":        rel,
		"props_json": propsJSON,
	}, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	return err
}

// Neighbors returns 1-hop entities around id.
func (s *Store) Neighbors(ctx context.Context, id string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 25
	}
	cypher := `
MATCH (e:Entity {id: $id})-[l:LINK]-(n:Entity)
RETURN DISTINCT n.id AS id, n.name AS name, n.kind AS kind, l.name AS rel
LIMIT $limit`
	res, err := neo4j.ExecuteQuery(ctx, s.Driver, cypher, map[string]any{"id": id, "limit": limit}, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(s.Database))
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, rec := range res.Records {
		row := map[string]any{}
		keys := rec.Keys
		if len(keys) == 0 {
			keys = res.Keys
		}
		for _, k := range keys {
			v, _ := rec.Get(k)
			row[k] = v
		}
		out = append(out, row)
	}
	return out, nil
}
