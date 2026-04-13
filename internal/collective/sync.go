package collective

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/approval"
)

// PushProposal sends a pending proposal to org collective (stub HTTP); nodes push signals up.
func PushProposal(baseURL string, p approval.Proposal) error {
	return postCollective(baseURL, "/v1/ingest/proposal", p)
}

// PushApprovedMutation notifies the collective that a proposal was applied locally (skills, tools, memory ops, harness).
// Enterprise mirrors can subscribe (e.g. NATS) and distribute vetted artifacts fleet-wide.
func PushApprovedMutation(baseURL string, p approval.Proposal) error {
	return postCollective(baseURL, "/v1/ingest/approved-mutation", p)
}

func postCollective(baseURL, path string, p approval.Proposal) error {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil
	}
	base = strings.TrimRight(base, "/")
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("collective %s: %s", resp.Status, string(body))
	}
	return nil
}

// PullSkillGraph fetches org-wide skill graph snapshot (optional).
func PullSkillGraph(baseURL string) ([]byte, error) {
	if baseURL == "" {
		return nil, nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(baseURL + "/v1/skills/graph")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
