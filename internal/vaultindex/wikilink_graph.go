package vaultindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/infra/neo4jstore"
	"github.com/heros-foreal/agentd/internal/memorylayer"
)

// NoteEntityID is the graph entity id for an indexed vault note.
func NoteEntityID(tenantID, absVaultRoot, relPath string) string {
	return "vn-" + FileKey(tenantID, absVaultRoot, relPath)
}

func stubEntityID(tenantID, absVaultRoot, rawTarget string) string {
	t := strings.TrimSpace(tenantID)
	v := strings.ToLower(filepath.Clean(absVaultRoot))
	p := strings.ToLower(strings.TrimSpace(rawTarget))
	h := sha256.Sum256([]byte(t + "|" + v + "|" + p))
	return "vu-" + hex.EncodeToString(h[:8])
}

func edgeIDWikilink(srcID, dstID, rawInner, section string) string {
	h := sha256.Sum256([]byte(srcID + "\n" + dstID + "\n" + rawInner + "\n" + section))
	return "wl-" + hex.EncodeToString(h[:12])
}

func upsertVaultNoteEntity(ctx context.Context, db *sql.DB, neo *neo4jstore.Store, tenantID, absRoot, relPath string) error {
	id := NoteEntityID(tenantID, absRoot, relPath)
	name := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	props, _ := json.Marshal(map[string]any{
		"vault_root": filepath.ToSlash(absRoot),
		"rel_path":   filepath.ToSlash(relPath),
		"tenant_id":  tenantID,
	})
	if err := memorylayer.UpsertEntity(db, id, name, "vault_note", string(props)); err != nil {
		return err
	}
	if neo != nil {
		if err := neo.UpsertEntity(ctx, id, name, "vault_note", string(props)); err != nil {
			return err
		}
	}
	return nil
}

// syncWikilinks replaces WIKILINK edges from this note; creates dst stubs when unresolved.
func syncWikilinks(ctx context.Context, db *sql.DB, neo *neo4jstore.Store, tenantID, absRoot, srcRel, rawMarkdown string, ix *linkIndex) error {
	if ix == nil {
		return nil
	}
	srcID := NoteEntityID(tenantID, absRoot, srcRel)
	if err := upsertVaultNoteEntity(ctx, db, neo, tenantID, absRoot, srcRel); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM graph_edges WHERE src_id = ? AND rel = ?`, srcID, "WIKILINK"); err != nil {
		return err
	}
	if neo != nil {
		if err := neo.DeleteOutgoingLinksNamed(ctx, srcID, "WIKILINK"); err != nil {
			return err
		}
	}

	inners := ParseWikilinks(rawMarkdown)
	seenEdge := map[string]struct{}{}
	for _, inner := range inners {
		dstRel, section, _, ok := ix.resolve(srcRel, inner)
		var dstID string
		var dstName string
		var dstKind string
		var props map[string]any
		if ok {
			dstID = NoteEntityID(tenantID, absRoot, dstRel)
			dstName = strings.TrimSuffix(filepath.Base(dstRel), filepath.Ext(dstRel))
			dstKind = "vault_note"
			props = map[string]any{
				"vault_root": filepath.ToSlash(absRoot),
				"rel_path":   filepath.ToSlash(dstRel),
				"tenant_id":  tenantID,
			}
		} else {
			dstID = stubEntityID(tenantID, absRoot, inner)
			dstName = strings.TrimSpace(strings.Split(inner, "|")[0])
			if i := strings.Index(dstName, "#"); i >= 0 {
				dstName = strings.TrimSpace(dstName[:i])
			}
			if dstName == "" {
				dstName = "unresolved"
			}
			dstKind = "vault_unresolved"
			props = map[string]any{
				"vault_root": filepath.ToSlash(absRoot),
				"tenant_id":  tenantID,
				"raw_target": inner,
			}
		}
		pj, _ := json.Marshal(props)
		if err := memorylayer.UpsertEntity(db, dstID, dstName, dstKind, string(pj)); err != nil {
			return err
		}
		if neo != nil {
			if err := neo.UpsertEntity(ctx, dstID, dstName, dstKind, string(pj)); err != nil {
				return err
			}
		}

		edgeProps, _ := json.Marshal(map[string]any{
			"vault_root": filepath.ToSlash(absRoot),
			"src_rel":    filepath.ToSlash(srcRel),
			"dst_rel":    filepath.ToSlash(dstRel),
			"raw":        inner,
			"section":    section,
			"tenant_id":  tenantID,
			"resolved":   ok,
		})
		eid := edgeIDWikilink(srcID, dstID, inner, section)
		if _, hit := seenEdge[eid]; hit {
			continue
		}
		seenEdge[eid] = struct{}{}

		if err := memorylayer.Link(db, eid, srcID, dstID, "WIKILINK", string(edgeProps)); err != nil {
			return err
		}
		if neo != nil {
			if err := neo.Link(ctx, eid, srcID, dstID, "WIKILINK", string(edgeProps)); err != nil {
				return fmt.Errorf("neo4j wikilink: %w", err)
			}
		}
	}
	return nil
}

// removeVaultNoteFromGraph removes the note entity (SQLite CASCADE drops incident edges).
func removeVaultNoteFromGraph(ctx context.Context, db *sql.DB, neo *neo4jstore.Store, tenantID, absRoot, relPath string) error {
	id := NoteEntityID(tenantID, absRoot, relPath)
	if neo != nil {
		if err := neo.DeleteEntityDetach(ctx, id); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM graph_entities WHERE id = ?`, id); err != nil {
		return err
	}
	return nil
}
