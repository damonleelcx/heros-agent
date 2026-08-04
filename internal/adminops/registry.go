package adminops

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// registry.go is the model-registry administration surface (FR10): add and deprecate models, and
// repoint the per-model PRICE REFERENCES that feed SUM.
//
// # What a price REFERENCE is, and why the console never holds a price
//
// A price reference is an opaque handle into the billing provider's price catalogue
// ("price_ref_sonnet_input_v3"). The platform stores the handle; the provider holds the amount. That
// is the same posture P7 takes for plans, and it is what keeps every business number out of this
// repository's history — a value committed to git cannot be un-published.
//
// # Non-retroactivity, and why it is a snapshot rather than a rule
//
// Repointing a price reference must not rewrite a metering or billing period that has already closed:
// a closed period was reconciled against an invoice the customer already saw, and silently re-deriving
// it is a billing-integrity violation. That is enforced by SNAPSHOTTING each model's reference at the
// moment the period closes, and resolving a closed period's reference from the snapshot. A rule
// ("do not apply repoints backwards") would depend on every future reader remembering it; a snapshot
// is simply the answer.

// ModelRecord is one administered model and the price reference in force for it right now.
type ModelRecord struct {
	ModelID  string `json:"model_id"`
	Provider string `json:"provider"`
	// PriceRef is the OPAQUE provider price handle used to derive SUM. Never an amount.
	PriceRef string `json:"price_ref"`
	// Deprecated marks a model that must not be selected for new runs. It is not a deletion: closed
	// periods that used it still have to resolve, and a registry that forgets a model cannot explain
	// last quarter's SUM.
	Deprecated   bool      `json:"deprecated"`
	DeprecatedAt time.Time `json:"deprecated_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Revision increments on every administered change, so an operator can tell a stale read from a
	// current one without comparing every field.
	Revision int `json:"revision"`
}

// Registry errors.
var (
	// ErrModelExists rejects adding a model that is already administered — silently overwriting one
	// would repoint its price reference as a side effect of an "add".
	ErrModelExists = errors.New("adminops: that model is already in the registry")
	// ErrNoSuchModel is returned for an unknown model id.
	ErrNoSuchModel = errors.New("adminops: no such model in the registry")
	// ErrPeriodClosed is returned when an administrative change is aimed at a closed period.
	ErrPeriodClosed = errors.New("adminops: that metering period is closed — closed periods keep the price reference in force when they closed")
)

// ModelRegistry is the administered model catalogue and its per-period price-reference history.
//
// It is CONFIGURATION: a deployment backs it with the same config store that serves plan definitions.
// The in-memory implementation here is the reference shape and the demo/test store; nothing about it
// is git-tracked, and no value it holds is an amount.
type ModelRegistry struct {
	mu     sync.RWMutex
	models map[string]ModelRecord
	// closed maps a closed period id to the price references in force at the moment it closed. This
	// is the whole of non-retroactivity.
	closed map[string]map[string]string
	order  []string
	rev    int
	now    func() time.Time
	// writer is the optional durable backing (registrydurable.go). Nil means the registry lives only as
	// long as the process — which on a WRITE surface whose contents decide what a run cost is silent
	// data loss, so `adminlaunch` refuses to mount it without one.
	writer ModelWriter
}

// NewModelRegistry builds an empty registry.
func NewModelRegistry(now func() time.Time) *ModelRegistry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ModelRegistry{models: map[string]ModelRecord{}, closed: map[string]map[string]string{}, now: now}
}

// Add records a new model with its price reference.
func (r *ModelRegistry) Add(modelID, provider, priceRef string) (ModelRecord, error) {
	if strings.TrimSpace(modelID) == "" || strings.TrimSpace(provider) == "" {
		return ModelRecord{}, errors.New("adminops: a model needs an id and a provider")
	}
	if strings.TrimSpace(priceRef) == "" {
		return ModelRecord{}, errors.New("adminops: a model needs a price reference — an unpriced model produces SUM gaps, not zero cost")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.models[modelID]; ok {
		return ModelRecord{}, fmt.Errorf("%w: %s", ErrModelExists, modelID)
	}
	r.rev++
	rec := ModelRecord{ModelID: modelID, Provider: provider, PriceRef: priceRef, UpdatedAt: r.now(), Revision: r.rev}
	// Durable first: if it did not persist, it did not happen. A model that exists until the next restart
	// changes what SUM derives to, with nothing logged at the moment it disappears.
	if err := r.persist(rec); err != nil {
		return ModelRecord{}, err
	}
	r.models[modelID] = rec
	return rec, nil
}

// persist write-throughs one record. The caller holds the lock.
//
// The revision counter is NOT rolled back on failure, for the reason adminrbac's grant sequence is not:
// a burnt revision costs nothing, while reusing one lets two different states of a model claim the same
// revision — and the revision is exactly what an operator compares to tell a stale read from a current
// one.
func (r *ModelRegistry) persist(rec ModelRecord) error {
	if r.writer == nil {
		return nil
	}
	if err := r.writer.PutModel(rec); err != nil {
		return fmt.Errorf("adminops: persist model %s: %w", rec.ModelID, err)
	}
	return nil
}

// Repoint changes a model's price reference. It affects the OPEN period and everything after it;
// closed periods keep what they closed with.
func (r *ModelRegistry) Repoint(modelID, priceRef string) (ModelRecord, error) {
	if strings.TrimSpace(priceRef) == "" {
		return ModelRecord{}, errors.New("adminops: a repoint needs the new price reference")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.models[modelID]
	if !ok {
		return ModelRecord{}, fmt.Errorf("%w: %s", ErrNoSuchModel, modelID)
	}
	r.rev++
	rec.PriceRef, rec.UpdatedAt, rec.Revision = priceRef, r.now(), r.rev
	if err := r.persist(rec); err != nil {
		return ModelRecord{}, err
	}
	r.models[modelID] = rec
	return rec, nil
}

// Deprecate marks a model unavailable for new runs. The record stays, so closed periods still resolve.
func (r *ModelRegistry) Deprecate(modelID string) (ModelRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.models[modelID]
	if !ok {
		return ModelRecord{}, fmt.Errorf("%w: %s", ErrNoSuchModel, modelID)
	}
	r.rev++
	rec.Deprecated, rec.DeprecatedAt, rec.Revision = true, r.now(), r.rev
	if err := r.persist(rec); err != nil {
		return ModelRecord{}, err
	}
	r.models[modelID] = rec
	return rec, nil
}

// ClosePeriod snapshots every model's current price reference and marks the period closed.
//
// Called by the metering close, not by an operator: closing a period is an accounting event, and the
// console administers the registry rather than the calendar.
func (r *ModelRegistry) ClosePeriod(periodID string) error {
	if strings.TrimSpace(periodID) == "" {
		return errors.New("adminops: closing a period needs the period id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, done := r.closed[periodID]; done {
		return nil // closing twice is a no-op, never a re-snapshot of today's references
	}
	snap := make(map[string]string, len(r.models))
	for id, rec := range r.models {
		snap[id] = rec.PriceRef
	}
	// 🔴 Durable first, and this is the write that matters most in the whole file. Non-retroactivity is
	// the promise that a closed period keeps the price references it closed with; a snapshot that lives
	// only in this process expires with it, and every closed period then silently re-resolves against
	// today's references.
	if r.writer != nil {
		if err := r.writer.ClosePeriod(periodID, snap); err != nil {
			return fmt.Errorf("adminops: persist closed period %s: %w", periodID, err)
		}
	}
	r.closed[periodID] = snap
	r.order = append(r.order, periodID)
	return nil
}

// PeriodClosed reports whether a period has been closed.
func (r *ModelRegistry) PeriodClosed(periodID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.closed[periodID]
	return ok
}

// ClosedPeriods lists closed periods in close order.
func (r *ModelRegistry) ClosedPeriods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// PriceRefAt resolves the price reference in force for a model in a period.
//
// For a CLOSED period it returns the snapshot taken at close; for an open or future period it returns
// the current reference. This one function is where non-retroactivity lives, so there is exactly one
// place to get it right.
func (r *ModelRegistry) PriceRefAt(modelID, periodID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if snap, ok := r.closed[periodID]; ok {
		ref, found := snap[modelID]
		return ref, found
	}
	rec, ok := r.models[modelID]
	if !ok {
		return "", false
	}
	return rec.PriceRef, true
}

// Get returns one model record.
func (r *ModelRegistry) Get(modelID string) (ModelRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.models[modelID]
	return rec, ok
}

// List returns every administered model in id order.
func (r *ModelRegistry) List() []ModelRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelRecord, 0, len(r.models))
	for _, rec := range r.models {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}

// Describe names the store for the readiness surface — never its contents.
func (r *ModelRegistry) Describe() string {
	if r.Durable() {
		return "postgres admin_model + admin_model_closed_price (survives a restart)"
	}
	return "config-store:model-registry(in-process)"
}

// ── The command surface ─────────────────────────────────────────────────────────────────────────

// RegistryService is the operator's model-registry administration surface.
type RegistryService struct {
	exec     *Executor
	registry *ModelRegistry
}

// NewRegistryService wires the service.
func NewRegistryService(exec *Executor, registry *ModelRegistry) (*RegistryService, error) {
	if exec == nil || registry == nil {
		return nil, errors.New("adminops: the registry service needs the command path and the model registry")
	}
	return &RegistryService{exec: exec, registry: registry}, nil
}

// List returns the administered models. Permission-gated on registry administration: the catalogue and
// its price references are operational configuration, not a public read.
func (s *RegistryService) List(ctx context.Context) ([]ModelRecord, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapRegistryAdmin, TargetGlobal); err != nil {
		return nil, err
	}
	return s.registry.List(), nil
}

// AddModel administers a new model and its price reference.
func (s *RegistryService) AddModel(ctx context.Context, modelID, provider, priceRef, reason string, confirm Confirmation) (Receipt, error) {
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapRegistryAdmin,
		Action:     adminaudit.ActionRegistryAddModel,
		Target:     ModelTarget(modelID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{modelID, provider, priceRef},
		Evidence:   map[string]string{"model_id": modelID, "provider": provider, "price_ref": priceRef},
	}, func(context.Context) (map[string]string, error) {
		rec, err := s.registry.Add(modelID, provider, priceRef)
		if err != nil {
			return nil, err
		}
		return map[string]string{"model_id": rec.ModelID, "price_ref": rec.PriceRef, "revision": fmt.Sprint(rec.Revision)}, nil
	})
}

// DeprecateModel marks a model unavailable for new runs. Closed-period SUM is unaffected: the record
// and its snapshotted references remain resolvable.
func (s *RegistryService) DeprecateModel(ctx context.Context, modelID, reason string, confirm Confirmation) (Receipt, error) {
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapRegistryAdmin,
		Action:     adminaudit.ActionRegistryDeprecate,
		Target:     ModelTarget(modelID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{modelID},
		Evidence:   map[string]string{"model_id": modelID},
	}, func(context.Context) (map[string]string, error) {
		rec, err := s.registry.Deprecate(modelID)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"model_id": rec.ModelID, "deprecated": "true",
			"closed_periods_unaffected": fmt.Sprint(len(s.registry.ClosedPeriods())),
		}, nil
	})
}

// RepointPriceRef repoints a model's price reference for the open and future periods.
func (s *RegistryService) RepointPriceRef(ctx context.Context, modelID, newPriceRef, reason string, confirm Confirmation) (Receipt, error) {
	before, ok := s.registry.Get(modelID)
	if !ok {
		return Receipt{}, fmt.Errorf("%w: %s", ErrNoSuchModel, modelID)
	}
	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapRegistryAdmin,
		Action:     adminaudit.ActionRegistryRepointPrice,
		Target:     ModelTarget(modelID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{modelID, newPriceRef},
		Evidence: map[string]string{
			"model_id": modelID, "from_price_ref": before.PriceRef, "to_price_ref": newPriceRef,
			"applies_to": "open and future periods only",
		},
	}, func(context.Context) (map[string]string, error) {
		rec, err := s.registry.Repoint(modelID, newPriceRef)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"model_id": rec.ModelID, "price_ref": rec.PriceRef, "revision": fmt.Sprint(rec.Revision),
			"closed_periods_retained": fmt.Sprint(len(s.registry.ClosedPeriods())),
		}, nil
	})
}
