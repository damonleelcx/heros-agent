package discovery

import "sort"

// MergeRecord documents that one node_id was found by more than one detection source (doc 09 dedup_merges).
type MergeRecord struct {
	NodeID  string            `json:"node_id"`
	Sources []DetectionSource `json:"sources"`
}

// Merge deduplicates detected call sites by node_id so that one call site == one node (§3.5, D1). A
// site hit by both the registry and a declaration becomes a single node crediting both sources, with a
// MergeRecord for the run report. Output is sorted by node_id for deterministic IR (I3).
func Merge(sites []DetectedCallSite) ([]DetectedCallSite, []MergeRecord) {
	index := map[string]int{}
	var out []DetectedCallSite
	for _, s := range sites {
		if j, ok := index[s.NodeID]; ok {
			out[j] = combineSites(out[j], s)
			continue
		}
		index[s.NodeID] = len(out)
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })

	var merges []MergeRecord
	for i := range out {
		if len(distinctSources(out[i].Sources)) > 1 {
			merges = append(merges, MergeRecord{NodeID: out[i].NodeID, Sources: out[i].Sources})
		}
	}
	return out, merges
}

// combineSites folds a duplicate detection of the same node into the surviving site. Registry-resolved
// metadata (arg map / provider / opacity) is preferred; a declaration fills what the registry left unset
// and always contributes its DetectOnly / Invocation intent. Both sources are credited.
func combineSites(dst, src DetectedCallSite) DetectedCallSite {
	dst.Sources = append(dst.Sources, src.Sources...)
	dst.Basis = append(dst.Basis, src.Basis...)
	if src.RegistryRow != "" {
		dst.RegistryRow = src.RegistryRow
	}
	if src.DeclaredSym != "" {
		dst.DeclaredSym = src.DeclaredSym
	}
	// Prefer a resolved (non-nil) arg map from whichever source has one; registry rows always do.
	if dst.ArgMap == (ArgMap{}) {
		dst.ArgMap = src.ArgMap
	}
	if dst.ProviderHint == "" {
		dst.ProviderHint = src.ProviderHint
	}
	if len(dst.Opacity) == 0 {
		dst.Opacity = src.Opacity
	}
	if src.DetectOnly {
		dst.DetectOnly = true
	}
	if dst.Invocation == "" {
		dst.Invocation = src.Invocation
	}
	return dst
}

func distinctSources(ss []DetectionSource) []DetectionSource {
	seen := map[DetectionSource]bool{}
	var out []DetectionSource
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
