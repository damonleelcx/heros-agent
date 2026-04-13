package vaultindex

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// FileKey is a stable hex id for (tenant, vault root, relative path) used in chunk ids and Qdrant payload.
func FileKey(tenantID, absVaultRoot, relPath string) string {
	t := strings.TrimSpace(tenantID)
	v := strings.ToLower(filepath.Clean(absVaultRoot))
	p := strings.ToLower(filepath.ToSlash(relPath))
	h := sha256.Sum256([]byte(t + "|" + v + "|" + p))
	return hex.EncodeToString(h[:8])
}

// ChunkSQLiteID is the primary key for semantic_chunks rows for vault chunks.
func ChunkSQLiteID(fileKey string, chunkIndex int) string {
	return "vk-" + fileKey + "-" + fmt.Sprintf("%06d", chunkIndex)
}
