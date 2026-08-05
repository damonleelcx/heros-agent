package tenancy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/lib/pq"
)

// pgerrors.go turns two Postgres SQLSTATEs into this package's own errors, and mints the identifiers
// the store assigns.
//
// # Why the SQLSTATE and not the message
//
// `strings.Contains(err.Error(), "duplicate key")` is the shape people reach for, and it breaks the
// first time a server runs under a non-English locale — silently, by classifying a duplicate as an
// unknown failure and surfacing a 500 where the caller had a perfectly good "already exists" branch.
// The SQLSTATE is a wire-level constant that no locale changes. The string form is kept only as a
// fallback for a driver that wraps the error past recognition, and it is deliberately second.

// isUniqueViolation reports whether err is SQLSTATE 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return strings.Contains(err.Error(), "23505")
}

// isForeignKeyViolation reports whether err is SQLSTATE 23503 (foreign_key_violation).
//
// Inside the identity domain this is never a user error to display verbatim: it means a membership,
// credential or session was written for an organization or a person that does not exist, which is a
// caller bug. It is mapped to ErrNotFound so the caller's own "no such organization" branch handles it
// rather than a 500 reaching a screen.
func isForeignKeyViolation(err error) bool {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return strings.Contains(err.Error(), "23503")
}

// newID mints a prefixed random identifier.
//
// Random rather than sequential, and the reason is the one `api_credential` cares about: a sequence lets
// an observer who sees two ids estimate how many the platform has issued between them. For a credential
// id that is a rough count of the customer base, given away for free.
func newID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition this package can paper over: every id it mints is
		// either a primary key or something an attacker must not be able to guess. Panicking here is
		// the honest outcome — the alternative is a predictable id that looks fine.
		panic("tenancy: crypto/rand is unavailable: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// NewID is the exported form, for callers that mint an id before handing a record to the store.
func NewID(prefix string) string { return newID(prefix) }
