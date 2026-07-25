package registry

import (
	"context"
	"fmt"
)

// Model-catalog read model (P10 M-series, task M3.1) — the matrix's ROWS.
//
// A read model over the model_entry rows that already exist (no new table). It lists the registered
// models, one entry per name at its latest version, with the provider/id/params a row needs to render
// and a run needs to execute. Models are a shared catalog, not tenant-scoped — a tenant picks from the
// same model registry every other phase resolves against.

// ModelCatalogEntry is one row of the matrix: a registered model at its latest version.
type ModelCatalogEntry struct {
	VersionID string      `json:"version_id"`
	Name      string      `json:"name"`
	Provider  string      `json:"provider"`
	ModelID   string      `json:"model_id"`
	Params    ModelParams `json:"params"`
}

// ModelCatalog returns the registered models, latest version per name, ordered by name. Empty (non-nil)
// when nothing is registered — an empty catalog is a legitimate matrix state (no rows), distinguishable
// from a retrieval failure.
func (s *Store) ModelCatalog(ctx context.Context) ([]ModelCatalogEntry, error) {
	// created_at DESC so the FIRST row seen per name is its latest version; DISTINCT ON keeps it.
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT ON (name) version_id, name, envelope
		   FROM model_entry
		  ORDER BY name, created_at DESC, version_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("registry: model catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ModelCatalogEntry{}
	for rows.Next() {
		var (
			versionID string
			name      string
			envelope  []byte
		)
		if err := rows.Scan(&versionID, &name, &envelope); err != nil {
			return nil, fmt.Errorf("registry: model catalog: scan: %w", err)
		}
		var spec ModelSpec
		if _, err := decodeEnvelope(KindModel, versionID, envelope, &spec); err != nil {
			return nil, err
		}
		out = append(out, ModelCatalogEntry{
			VersionID: versionID, Name: name,
			Provider: spec.Provider, ModelID: spec.ModelID, Params: spec.Params,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: model catalog: %w", err)
	}
	return out, nil
}
