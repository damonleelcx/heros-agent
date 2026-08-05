package api

import (
	"log"
	"strconv"
)

// accountshelpers.go holds the three small functions accounts.go needs and nothing else does.

// itoa and trimFloat exist so a seat message can name BOTH numbers without pulling fmt's verbs into a
// user-facing sentence. `trimFloat` renders 5 as "5" rather than "5.000000", because a plan allowance is
// a whole number of seats to everybody who reads it.
func itoa(n int) string { return strconv.Itoa(n) }

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// logSecurity records a refusal an operator should be able to read.
//
// 🔴 It takes an event NAME and an organization, and there is deliberately no parameter an identity, an
// address or a presented value could arrive in. The cause of an identity refusal belongs where an
// operator can read it and an attacker cannot — the same split `federation.ts` already makes — and a
// function with nowhere to put the value cannot leak it by accident.
func logSecurity(event, tenantID string) {
	log.Printf("security event: %s organization=%s", event, tenantID)
}

// logMemberRemoval records who removed whom, and how much actually stopped working.
//
// The two counts are here because "we removed them" is compatible with revoking nothing: a removal that
// reports success and leaves a session live is the failure the offboarding claim rests on, and the only
// way to see it after the fact is to have written down what it did.
func logMemberRemoval(tenantID, actorUserID, targetUserID string, sessions, credentials int) {
	log.Printf("member removed: organization=%s actor=%s subject=%s sessions_revoked=%d credentials_revoked=%d",
		tenantID, actorUserID, targetUserID, sessions, credentials)
}

// credentialKind is the word `whoami` reports: "personal" or "machine".
//
// It is derived from whether the principal names a person, which is the one fact that decides what
// member removal covers. A caller left to infer it from an absent user id will infer it wrong exactly
// once, at the moment it matters.
func credentialKind(p interface{ Personal() bool }) string {
	if p.Personal() {
		return "personal"
	}
	return "machine"
}
