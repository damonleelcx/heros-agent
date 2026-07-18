package telemetry

import (
	"fmt"
	"sync"
)

// PriceBook is the pinned price source for cost metrics (task 1.3: "Pin the price source ... the
// model-registry version").
//
// # Why price is NOT in config_hash, and why cost still has to be attributable
//
// A model's price is a deployment fact that changes over time; it is deliberately kept OUT of the
// model version envelope and config_hash (registry/model.go: the envelope hashes provider + model_id +
// params, not price) so that a vendor price change does not orphan every stored result under a new
// hash. But a cost figure must still be reproducible: "$0.004" is meaningless unless you can say which
// price table produced it. So every PriceBook carries a Version, and every cost metric event carries
// that version as a dimension (AttrPriceBookVer). Cost is then attributable to an exact {config_hash,
// pricebook_version} pair — reproducible without polluting config_hash.
//
// Lookup is by (provider, model_id): a model version pins its provider+model_id, so this resolves the
// same price for every config that references that model version, which is what lets cost roll up
// across configs and seeds.
type PriceBook struct {
	version string

	mu     sync.RWMutex
	prices map[string]ModelInfo // key: provider + "/" + model_id
}

// ModelInfo is one model's price and context window. Prices are USD per MILLION tokens (the unit
// vendors publish), converted to a per-token figure at cost time.
type ModelInfo struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
	// ContextWindow is the model's max context in tokens, for context-window utilization (task 1.4).
	// Zero means unknown — utilization is then not computable and is emitted as a documented gap rather
	// than a misleading 0 (see MetricSet).
	ContextWindow int
}

// NewPriceBook builds a price source with a version label. The version is required: an unversioned
// price book produces cost figures nothing can reproduce, which is the exact un-attributability this
// whole substrate exists to prevent.
func NewPriceBook(version string) (*PriceBook, error) {
	if version == "" {
		return nil, fmt.Errorf("telemetry: PriceBook requires a version so cost figures are attributable")
	}
	return &PriceBook{version: version, prices: map[string]ModelInfo{}}, nil
}

// Version is the pinned identifier stamped onto every cost metric this book prices.
func (p *PriceBook) Version() string { return p.version }

func priceKey(provider, modelID string) string { return provider + "/" + modelID }

// Set records a model's price and context window. Overwriting is allowed only for building a book
// before use; the version is what makes a change attributable, so a genuine price change ships as a
// new PriceBook version, not a silent mutation of a live one.
func (p *PriceBook) Set(provider, modelID string, info ModelInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prices[priceKey(provider, modelID)] = info
}

// Lookup returns a model's ModelInfo, reporting whether it is priced. A miss is NOT a zero: an unknown
// model's cost is unknown, and emitting $0 would understate spend precisely where a new/unpriced model
// is in play. Callers surface the miss (a cost gap) rather than fabricate a number.
func (p *PriceBook) Lookup(provider, modelID string) (ModelInfo, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	info, ok := p.prices[priceKey(provider, modelID)]
	return info, ok
}

// costUSD computes the dollar cost of one call's token usage under this book. ok is false when the
// model is unpriced, so the caller can record a gap instead of a fake 0.
func (p *PriceBook) costUSD(provider, modelID string, u tokenUsage) (cost float64, ctxWindow int, ok bool) {
	info, found := p.Lookup(provider, modelID)
	if !found {
		return 0, 0, false
	}
	const perM = 1_000_000.0
	cost = float64(u.input)*info.InputPerMTok/perM +
		float64(u.output)*info.OutputPerMTok/perM +
		float64(u.cacheRead)*info.CacheReadPerMTok/perM +
		float64(u.cacheWrite)*info.CacheWritePerMTok/perM
	return cost, info.ContextWindow, true
}

// tokenUsage is the gateway's normalized Usage, restated locally so pricing does not import the
// gateway (it is imported the other way — the instrument bridges the two).
type tokenUsage struct {
	input, output, thinking, cacheRead, cacheWrite int
}
