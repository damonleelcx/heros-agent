package adminops

import (
	"errors"
	"fmt"
)

// registrydurable.go gives the model registry an optional durable backing.
//
// It is the same write-through port `adminidentity/durable.go` documents, in the same order — persist
// FIRST, and a failed write aborts the mutation with memory untouched — for a reason specific to this
// store: the registry is a WRITE surface whose contents decide what a run cost. An operator adds a
// model and its price reference, the pod restarts, and not only is the work gone, but SUM silently
// re-derives against a registry that no longer contains the model. Nothing errors; the number just
// changes.
//
// `ClosePeriod` is the one that must never be lost. Non-retroactivity is the promise that a closed
// metering period keeps the price references it closed with, and held in a map that promise expired
// with the process.

// ModelWriter persists registry mutations. No context parameter, matching the store's own methods.
type ModelWriter interface {
	// PutModel inserts or replaces one model record.
	PutModel(m ModelRecord) error
	// ClosePeriod records the price references in force at the moment a period closed.
	ClosePeriod(periodID string, priceRefs map[string]string) error
}

// SetWriter attaches a durable backing. Refuses a second one, for the reason the identity directories
// do: a swapped backing leaves what the store already wrote in the previous one, invisibly.
func (r *ModelRegistry) SetWriter(w ModelWriter) error {
	if w == nil {
		return errors.New("adminops: SetWriter(nil) — leave the writer unset for an in-memory registry rather than clearing one")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writer != nil {
		return errors.New("adminops: this model registry already has a durable backing — a second one would leave the models written to the first invisible")
	}
	r.writer = w
	return nil
}

// Durable reports whether the registry survives a restart.
func (r *ModelRegistry) Durable() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.writer != nil
}

// LoadModels replays durably-held records WITHOUT persisting them again.
//
// `closed` maps a closed period id to the price references in force when it closed. Both are replayed
// together because a registry holding models but not its closed periods would answer PriceRefAt with
// today's reference for a period that closed months ago — which is exactly the retroactive change the
// second table exists to prevent, reintroduced at every boot.
func LoadModels(r *ModelRegistry, models []ModelRecord, closed map[string]map[string]string) error {
	if r == nil {
		return errors.New("adminops: LoadModels needs a registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range models {
		if m.ModelID == "" || m.Provider == "" {
			return fmt.Errorf("adminops: durable model row %q is missing a model id or provider", m.ModelID)
		}
		r.models[m.ModelID] = m
		if m.Revision > r.rev {
			r.rev = m.Revision
		}
	}
	for periodID, refs := range closed {
		if periodID == "" {
			return errors.New("adminops: a durable closed-period row has no period id")
		}
		copied := make(map[string]string, len(refs))
		for k, v := range refs {
			copied[k] = v
		}
		r.closed[periodID] = copied
	}
	return nil
}
