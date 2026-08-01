package api

import (
	"os"
	"path/filepath"
)

// readSource reads a file from this package's own directory, for the assertions that fence a SHAPE
// rather than a behaviour — "the login body has no `challenge` field" is an absence, and an absence is
// honestly checked by reading the source rather than by hoping a request exercises it.
func readSource(name string) (string, error) {
	body, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
