package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// EvalSetFile is where a published set lands in the customer's repository.
//
// 🔴 A file in their repo rather than a record in our database. An eval set they cannot read, diff,
// edit and commit is one they cannot own — and the whole point is that they run it, in their harness,
// with their credentials. A set that lives only here would make them ask us for their own tests.
const EvalSetFile = "heros-evalset.json"

// PublishEvalSet writes the set into the repository.
//
// Effect-bearing: it creates a file in somebody's working tree. It is therefore gated by the worker's
// approval policy and carries an idempotency key, and it has a Verifier — see NewPublishVerifier.
type PublishEvalSet struct {
	// Root is the repository. Injected per subject, like the assessment source.
	Root string
}

func (PublishEvalSet) Spec() toolcontract.Spec {
	return toolcontract.Spec{
		Kind:          planner.KindPublishEvalSet,
		Permissions:   []toolcontract.Permission{toolcontract.WriteSource},
		Timeout:       30 * time.Second,
		RetrySafe:     false,
		EffectBearing: true,
	}
}

func (p PublishEvalSet) Execute(_ context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	if p.Root == "" {
		return toolcontract.Result{}, fmt.Errorf("no repository is loaded, so there is nowhere to publish")
	}
	set, err := setFromInputs(c.Inputs)
	if err != nil {
		return toolcontract.Result{}, err
	}
	body, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return toolcontract.Result{}, err
	}
	body = append(body, '\n')

	full, err := safeJoin(p.Root, EvalSetFile)
	if err != nil {
		return toolcontract.Result{}, err
	}
	// Written through a temporary file and renamed, so a crash mid-write cannot leave a half-written
	// eval set that parses as a shorter one.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".heros-evalset-*")
	if err != nil {
		return toolcontract.Result{}, fmt.Errorf("publishing: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return toolcontract.Result{}, fmt.Errorf("publishing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return toolcontract.Result{}, fmt.Errorf("publishing: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return toolcontract.Result{}, fmt.Errorf("publishing: %w", err)
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return toolcontract.Result{}, fmt.Errorf("publishing: %w", err)
	}
	out, _ := json.Marshal(map[string]any{
		"path": EvalSetFile, "cases": len(set.Cases), "by_origin": set.ByOrigin, "missing": set.Missing,
	})
	return toolcontract.Result{Output: out}, nil
}

// setFromInputs decodes the gated set from the quality gate's result.
func setFromInputs(inputs map[string][]byte) (EvalSet, error) {
	for _, raw := range inputs {
		var set EvalSet
		if len(raw) == 0 || json.Unmarshal(raw, &set) != nil || len(set.Cases) == 0 {
			continue
		}
		return set, nil
	}
	return EvalSet{}, fmt.Errorf("the quality gate produced no set to publish")
}

// safeJoin refuses a path that leaves the repository, on RESOLVED paths.
func safeJoin(root, name string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("publishing: %s: %w", root, err)
	}
	full := filepath.Join(realRoot, filepath.FromSlash(name))
	rel, err := filepath.Rel(realRoot, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("publishing: %s leaves the repository", name)
	}
	return full, nil
}

// publishVerifier confirms the file is on disk and contains the cases that were approved.
//
// 🔴 Required by the tool contract for any effect-bearing tool, and it goes and LOOKS. A write that
// returned no error is not evidence the file says what you think — and this one is the artefact the
// customer is going to run.
type publishVerifier struct{ Root string }

// NewPublishVerifier builds the verifier for a repository.
func NewPublishVerifier(root string) toolcontract.Verifier { return publishVerifier{Root: root} }

func (v publishVerifier) Verify(_ context.Context, _ toolcontract.Call, r toolcontract.Result) (bool, string, error) {
	var claimed struct {
		Cases int `json:"cases"`
	}
	if err := json.Unmarshal(r.Output, &claimed); err != nil {
		return false, "the publish step reported nothing readable", nil
	}
	full, err := safeJoin(v.Root, EvalSetFile)
	if err != nil {
		// 🔴 Inconclusive, not absent. "I could not check" and "it is not there" lead to different next
		// actions, and a retry on the first is safe while a retry on the second may duplicate an effect.
		return false, "", err
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return false, fmt.Sprintf("%s is not on disk after publishing", EvalSetFile), nil
	}
	var set EvalSet
	if err := json.Unmarshal(body, &set); err != nil {
		return false, fmt.Sprintf("%s is not readable JSON", EvalSetFile), nil
	}
	if len(set.Cases) != claimed.Cases {
		return false, fmt.Sprintf("%s holds %d cases, %d were approved",
			EvalSetFile, len(set.Cases), claimed.Cases), nil
	}
	return true, "", nil
}
