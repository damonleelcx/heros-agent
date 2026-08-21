package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// connections.go is the repository-connection surface: list, connect, revoke, and read the ledger.
//
// # Why the console reads a VIEW rather than the domain type
//
// `sourceingest.Connection` is the grant. What the console renders is the grant PLUS the last
// successful read and the last failure with its cause (task 6.1), which are ledger facts. Sending the
// domain type and making the browser join two arrays would put a rule — "the last failure is the newest
// record whose outcome is not `succeeded`" — in a place where nobody reviews it and where a second
// consumer would implement it differently.
//
// # 🔴 What this surface can NEVER carry
//
// A forge credential, in either direction. The connect payload's `token` is the only place one appears,
// it is passed straight through to the secret store's `Store` inside the service, and NOTHING reads it
// back — there is no field on any view type that could hold it, and `TestNoConnectionViewFieldCanCarry
// ACredential` reads the view structs by reflection to keep it that way.
//
// `DisallowUnknownFields` on the connect payload is a privacy control rather than hygiene, for
// verdictingest.go's reason: the field a future client would add here is a scope or a second
// repository, and ignoring it silently is how a broader grant gets recorded as a narrow one.

// ConnectionSource is the P32 dependency: the connection lifecycle.
//
// An interface rather than the concrete `*sourceingest.Service` so a deployment can mount the read
// surface over a store it does not let this process write to — and so the handler tests can drive
// every refusal without a database.
type ConnectionSource interface {
	List(ctx context.Context, tenantID string) ([]sourceingest.Connection, error)
	Connect(ctx context.Context, req sourceingest.ConnectRequest) (sourceingest.Connection, error)
	Revoke(ctx context.Context, tenantID, connectionID string) (sourceingest.RevocationResult, error)
	Records(ctx context.Context, tenantID, connectionID string, limit int) ([]sourceingest.CloneRecord, error)
}

// ── Console view types (registered in consoletypes.go; rendered by web/console) ──

// ConnectionView is one connection as the console renders it.
type ConnectionView struct {
	ConnectionID string `json:"connection_id"`
	WorkflowID   string `json:"workflow_id"`
	// Mode is `connected` — the vocabulary the console switches on, alongside `bundle` and `local`
	// for workflows with no connection. Present on the row so the list does not have to infer it.
	Mode       sourceingest.Mode      `json:"mode"`
	Forge      sourceingest.Forge     `json:"forge"`
	Repository string                 `json:"repository"`
	SubPath    string                 `json:"sub_path,omitempty"`
	GrantKind  sourceingest.GrantKind `json:"grant_kind"`
	// GrantLabel and RevokeHint come from the forge adapter, so the console states what THIS forge's
	// grant permits rather than a generic sentence true of none of them.
	GrantLabel string `json:"grant_label"`
	RevokeHint string `json:"revoke_hint"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  int64  `json:"created_at_ms"`
	// LastSuccessAt is when the repository was last read successfully, or 0 if never.
	//
	// 🔴 Zero is rendered as `never read` rather than as an epoch date. A surface that has never
	// succeeded and one that succeeded in 1970 are different facts, and only one of them is possible.
	LastSuccessAt int64 `json:"last_success_at_ms"`
	// LastSuccessRevision is the revision of that read.
	LastSuccessRevision string `json:"last_success_revision,omitempty"`
	// LastFailureAt and LastFailureCause are the newest failure, or empty when there has been none.
	// The CAUSE is one of the four; the console renders four messages and never one.
	LastFailureAt    int64                   `json:"last_failure_at_ms"`
	LastFailureCause sourceingest.CloneCause `json:"last_failure_cause,omitempty"`
	// LastActor is `person` or `scheduled` for the most recent read of any outcome — the disclosure
	// made visible after the fact, not only before it.
	LastActor sourceingest.Actor `json:"last_actor,omitempty"`
}

// ConnectionsView is the connection surface's read model.
type ConnectionsView struct {
	Connections []ConnectionView `json:"connections"`
	// Forges is every forge that can be connected, with what its grant permits. Served with the list
	// so the consent screen has its copy without a second round trip — and so the copy comes from the
	// adapter that builds the grant rather than from a string in a TSX file.
	Forges []sourceingest.ForgeDescription `json:"forges"`
	// LocalModeDeployments states which deployments the local mode works against, BEFORE the flow
	// starts (FR15, PRD §14 A1). Empty when this deployment supports none, which the console renders
	// as a stated limit rather than as a flow that fails at its last step.
	LocalModeDeployments []string `json:"local_mode_deployments"`
	// RetentionHours is the cloned-snapshot window, published so the consent screen states the number
	// rather than the word "briefly".
	RetentionHours int64 `json:"retention_hours"`
}

// CloneRecordView is one entry in the read ledger.
type CloneRecordView struct {
	RecordID   string               `json:"record_id"`
	Repository string               `json:"repository"`
	Revision   string               `json:"revision"`
	Actor      sourceingest.Actor   `json:"actor"`
	ActorID    string               `json:"actor_id,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Outcome    sourceingest.Outcome `json:"outcome"`
	Bytes      int64                `json:"bytes"`
	Entries    int                  `json:"entries"`
	DurationMS int64                `json:"duration_ms"`
	At         int64                `json:"at_ms"`
}

// CloneLedgerView is a connection's read ledger, newest first.
type CloneLedgerView struct {
	ConnectionID string            `json:"connection_id"`
	Records      []CloneRecordView `json:"records"`
}

// RevocationView is what a revocation removed — a receipt, so the console's confirmation ("derived
// trees are deleted") can show a number instead of repeating a claim.
type RevocationView struct {
	ConnectionID     string `json:"connection_id"`
	SnapshotsDeleted int    `json:"snapshots_deleted"`
}

// connectPayload is the connect request's wire contract.
//
// 🚫 It has NO tenant field. The tenant comes from the authenticated principal, and the strongest form
// of "this request cannot widen its scope" is having no field it could widen it in.
type connectPayload struct {
	WorkflowID string `json:"workflow_id"`
	// Repository is the one the customer NAMED. The breadth check compares the forge's answer to
	// this, so it must be the customer's own input.
	Repository string `json:"repository"`
	SubPath    string `json:"sub_path,omitempty"`
	Forge      string `json:"forge"`
	GrantKind  string `json:"grant_kind"`
	ExternalID string `json:"external_id,omitempty"`
	// Covers is what the forge reports the grant reaches, and AccountWide whether it scoped the grant
	// to an account rather than a repository list. Both are required for the breadth refusal to be
	// possible at all — a payload carrying only the request would make the comparison unwritable.
	Covers      []string `json:"covers"`
	AccountWide bool     `json:"account_wide"`
	Scopes      []string `json:"scopes,omitempty"`
	// Token is the credential the forge issued. Passed straight to the secret store and never read
	// back by anything.
	Token string `json:"token"`
	// ConsentShown asserts the disclosure was displayed (FR10). The service refuses without it — see
	// sourceingest.Service.Connect for why that check is at the write and not only in the browser.
	ConsentShown bool `json:"consent_shown"`
}

// MountConnections registers the connection routes. Optional, like every other mount.
//
// P32 · the four routes are FLAT for the reason P29 gives: a path segment carrying an identifier
// needs a `pathType: Prefix` ingress rule, and a prefix on the public hostname exposes everything
// under it. `connection_id` travels in the JSON body for the two writes and in the query string for
// the one read that needs it — and that read is console→agentd inside the cluster, so it is
// deliberately absent from the public ingress.
func (s *Server) MountConnections(src ConnectionSource) {
	s.connections = src
	s.Mux.HandleFunc("GET /api/v1/repo-connections", s.handleConnectionList)
	s.Mux.HandleFunc("POST /api/v1/repo-connections", s.handleConnectionCreate)
	s.Mux.HandleFunc("POST /api/v1/repo-connection-revocations", s.handleConnectionRevoke)
	s.Mux.HandleFunc("GET /api/v1/repo-connection-reads", s.handleConnectionLedger)
}

// LocalModeDeployments names the deployments the local mode works against (PRD §14 A1).
//
// 🔴 It is a compiled-in constant rather than configuration, and that is the whole point. `heros link`
// is pinned to this host and the pin is enforced twice; making this list configurable would create the
// appearance that a self-hosted endpoint can be named, which is the boundary decision §14 A1
// deliberately did not take. The console reads this and states it BEFORE the pairing flow, so a
// customer on a self-hosted deployment learns it at step zero instead of at the last step.
var LocalModeDeployments = []string{"https://heros-agent.space"}

func (s *Server) connectionTenant(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.connections == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment does not offer repository connections — push a source bundle instead",
		})
		return auth.Principal{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "repository connections require an authenticated tenant"})
		return auth.Principal{}, false
	}
	return principal, true
}

func (s *Server) handleConnectionList(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.connectionTenant(w, r)
	if !ok {
		return
	}
	conns, err := s.connections.List(r.Context(), principal.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the repository connections could not be read"})
		return
	}
	view := ConnectionsView{
		Connections:          make([]ConnectionView, 0, len(conns)),
		Forges:               sourceingest.DescribeForges(),
		LocalModeDeployments: append([]string(nil), LocalModeDeployments...),
		RetentionHours:       int64(sourceingest.DefaultCloneRetention.Hours()),
	}
	for _, c := range conns {
		// The ledger is read per connection to compute the two "last" facts. A join would be one
		// query, and it is not worth a second SQL shape here: the list is a handful of rows per
		// tenant, and the loop keeps the rule ("last failure = newest non-succeeded record") in one
		// readable place rather than inside a window function.
		records, rerr := s.connections.Records(r.Context(), principal.TenantID, c.ConnectionID, 50)
		if rerr != nil {
			// 🔴 The connection is still rendered. A ledger that could not be read is a missing
			// ANNOTATION on a grant that certainly exists, and dropping the row would tell the
			// customer they have no connection — which is the one thing that must never be said
			// wrongly about a standing capability.
			records = nil
		}
		view.Connections = append(view.Connections, connectionViewOf(c, records))
	}
	writeJSON(w, http.StatusOK, view)
}

// connectionViewOf joins a grant with its ledger.
func connectionViewOf(c sourceingest.Connection, records []sourceingest.CloneRecord) ConnectionView {
	v := ConnectionView{
		ConnectionID: c.ConnectionID,
		WorkflowID:   c.WorkflowID,
		Mode:         sourceingest.ModeConnected,
		Forge:        c.Forge,
		Repository:   c.Repository,
		SubPath:      c.SubPath,
		GrantKind:    c.GrantKind,
		CreatedBy:    c.CreatedBy,
		CreatedAt:    c.CreatedAtMS,
	}
	if d, err := sourceingest.DescribeForge(c.Forge); err == nil {
		v.GrantLabel = d.GrantLabel
		v.RevokeHint = d.RevokeHint
	}
	// Records arrive newest first, so the first match in each direction is the newest one.
	for _, r := range records {
		if v.LastActor == "" {
			v.LastActor = r.Actor
		}
		if r.Outcome == sourceingest.OutcomeSucceeded {
			if v.LastSuccessAt == 0 {
				v.LastSuccessAt = r.AtMS
				v.LastSuccessRevision = r.Revision
			}
			continue
		}
		if v.LastFailureAt == 0 {
			v.LastFailureAt = r.AtMS
			v.LastFailureCause = sourceingest.CloneCause(r.Outcome)
		}
	}
	return v
}

// 🚫 The mode vocabulary is NOT redeclared here. It lives in `sourceingest.Mode`, which owns the
// concept and whose accessor the console's type generator reads — a second copy in this package would
// be a second closed set, and the copy is what goes stale.

func (s *Server) handleConnectionCreate(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.connectionTenant(w, r)
	if !ok {
		return
	}
	var p connectPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "the connection request could not be read — it carries a field outside the contract, or is malformed",
		})
		return
	}
	conn, err := s.connections.Connect(r.Context(), sourceingest.ConnectRequest{
		TenantID:   principal.TenantID,
		WorkflowID: p.WorkflowID,
		Repository: p.Repository,
		SubPath:    p.SubPath,
		// 🔴 The person, from the CREDENTIAL, and empty for a machine credential rather than a
		// placeholder. An audit entry naming a person who did not act is worse than one naming
		// none, because it is believed.
		CreatedBy:    principal.UserID,
		ConsentShown: p.ConsentShown,
		Authorization: sourceingest.Authorization{
			Forge:       sourceingest.Forge(p.Forge),
			GrantKind:   sourceingest.GrantKind(p.GrantKind),
			ExternalID:  p.ExternalID,
			Covers:      p.Covers,
			AccountWide: p.AccountWide,
			Token:       p.Token,
			Scopes:      p.Scopes,
		},
	})
	switch {
	case errors.Is(err, sourceingest.ErrGrantTooBroad):
		// 422, not 400. The request is well-formed and was understood; what is refused is the GRANT,
		// and the customer's next action is to narrow it on the forge — which "bad request" would not
		// tell them.
		writeJSON(w, http.StatusUnprocessableEntity, specError{Error: err.Error()})
	case errors.Is(err, sourceingest.ErrConnectionExists):
		writeJSON(w, http.StatusConflict, specError{Error: err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
	default:
		// 201: the grant exists now. Nothing has been read yet, and the response says so by carrying
		// `last_success_at_ms: 0` rather than by implying a clone happened.
		writeJSON(w, http.StatusCreated, connectionViewOf(conn, nil))
	}
}

// revokePayload names the connection to revoke.
type revokePayload struct {
	ConnectionID string `json:"connection_id"`
}

func (s *Server) handleConnectionRevoke(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.connectionTenant(w, r)
	if !ok {
		return
	}
	var p revokePayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the revocation request could not be read"})
		return
	}
	res, err := s.connections.Revoke(r.Context(), principal.TenantID, p.ConnectionID)
	switch {
	case errors.Is(err, sourceingest.ErrNoConnection):
		writeJSON(w, http.StatusNotFound, specError{Error: "no such repository connection"})
	case err != nil:
		// 🔴 500 with the reason, and the reason matters: a partially-completed cascade is retryable,
		// and the message says so. Reporting a bare "revocation failed" would leave a customer who
		// asked us to stop holding their source with no way to know whether we did.
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
	default:
		writeJSON(w, http.StatusOK, RevocationView{
			ConnectionID: res.ConnectionID, SnapshotsDeleted: res.SnapshotsDeleted,
		})
	}
}

func (s *Server) handleConnectionLedger(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.connectionTenant(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("connection_id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "connection_id is required"})
		return
	}
	records, err := s.connections.Records(r.Context(), principal.TenantID, id, 100)
	switch {
	case errors.Is(err, sourceingest.ErrNoConnection):
		writeJSON(w, http.StatusNotFound, specError{Error: "no such repository connection"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the read ledger could not be read"})
		return
	}
	view := CloneLedgerView{ConnectionID: id, Records: make([]CloneRecordView, 0, len(records))}
	for _, rec := range records {
		view.Records = append(view.Records, CloneRecordView{
			RecordID: rec.RecordID, Repository: rec.Repository, Revision: rec.Revision,
			Actor: rec.Actor, ActorID: rec.ActorID, Reason: rec.Reason,
			Outcome: rec.Outcome, Bytes: rec.Bytes, Entries: rec.Entries,
			DurationMS: rec.DurationMS, At: rec.AtMS,
		})
	}
	writeJSON(w, http.StatusOK, view)
}
