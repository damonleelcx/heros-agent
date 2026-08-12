package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// P30 task 9.1 at the HTTP boundary: what `/readyz` says about the agent, and — more importantly —
// what it does NOT do to the deployment's overall status.

func readyzWith(t *testing.T, r herosagent.Readiness) map[string]any {
	t.Helper()
	s := New(nil, config.Config{})
	s.SetAgentReadiness(func(context.Context) herosagent.Readiness { return r })

	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /readyz: %v (%s)", err, rec.Body.String())
	}
	body["__status_code"] = rec.Code
	return body
}

// 🔴 NONE OF THE AGENT'S STATES TAKES THE DEPLOYMENT DOWN.
//
// `disabled` is the DEFAULT (Q2), so gating on it would page somebody about the configuration every
// deployment ships with. `capped` is a ceiling working as intended. Even `credential_unresolved` is
// contained: HEROS is optional, every other surface is rule-derived, and taking a platform down because
// an optional subsystem cannot reach its vendor is a bigger outage than the one being reported.
func TestNoAgentStateMakesTheDeploymentNotReady(t *testing.T) {
	for _, state := range herosagent.ReadyStates() {
		body := readyzWith(t, herosagent.Readiness{State: state, Detail: "a detail"})
		if code := body["__status_code"].(int); code != http.StatusOK {
			t.Errorf("state %q answered %d — the agent is optional and none of its states may take the "+
				"deployment down", state, code)
		}
		if got := body["status"]; got != "ready" {
			t.Errorf("state %q made the overall status %v", state, got)
		}
	}
}

// It is reported at the TOP LEVEL, beside `secrets_source`, and 🚫 NOT inside `components` — every
// entry in that map is a gate, and this must not be one.
func TestTheAgentEntryIsNotAGatedComponent(t *testing.T) {
	body := readyzWith(t, herosagent.Readiness{
		State: herosagent.ReadyCredentialUnresolved, Detail: "the secret is missing",
	})
	agent, ok := body["heros_agent"].(map[string]any)
	if !ok {
		t.Fatal("/readyz carries no `heros_agent` entry")
	}
	if agent["state"] != string(herosagent.ReadyCredentialUnresolved) {
		t.Errorf("state is %v", agent["state"])
	}
	if agent["detail"] == "" {
		t.Error("the entry carries no detail — a state with no sentence is a word somebody has to look " +
			"up during an incident")
	}
	if components, ok := body["components"].(map[string]any); ok {
		if _, present := components["heros_agent"]; present {
			t.Error("the agent is inside `components`, where every entry is a GATE. A deployment whose " +
				"agent cannot reach its vendor would report itself not-ready, which is a bigger outage " +
				"than the one being reported.")
		}
	}
}

// A deployment that wires no readiness function reports NO entry at all — distinct from `disabled`,
// which is a setting somebody chose.
func TestADeploymentWithNoAgentReportsNoEntry(t *testing.T) {
	s := New(nil, config.Config{})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if strings.Contains(rec.Body.String(), "heros_agent") {
		t.Errorf("an unwired deployment reported an agent entry: %s", rec.Body.String())
	}
}

// 🚫 The entry carries no credential and nothing key-shaped. `CredentialSource` names the KIND of
// source; `/readyz` is unauthenticated, and a health endpoint must not be a reconnaissance surface.
func TestTheAgentReadinessEntryLeaksNoCredential(t *testing.T) {
	body := readyzWith(t, herosagent.Readiness{
		State: herosagent.ReadyReady, Detail: "all good", CredentialSource: "aws_secrets_manager",
		ConfigHash: "cfg-abc",
	})
	raw, err := json.Marshal(body["heros_agent"])
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"sk-", "sk_", "Bearer ", "api_key", "secret_value", "password"} {
		if strings.Contains(string(raw), shape) {
			t.Errorf("the entry carries %q: %s", shape, raw)
		}
	}
}
