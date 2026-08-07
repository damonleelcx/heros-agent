package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// bare404 answers every request the way a reverse proxy with no rule for the path does: a 404 carrying
// the console app's HTML not-found page. No JSON, no `error` key, nothing a platform handler wrote.
//
// This IS the production failure, reproduced. `/api/v1/workflows/{id}/ir` was absent from the ingress
// manifest, so the request fell through the `- path: /` prefix rule to Next.js, which rendered exactly
// this.
type bare404 struct {
	contentType string
	body        string
}

func (b bare404) RoundTrip(req *http.Request) (*http.Response, error) {
	ct, body := b.contentType, b.body
	if ct == "" {
		ct = "text/html; charset=utf-8"
	}
	if body == "" {
		body = "<!DOCTYPE html><html><body><h1>404</h1><p>This page could not be found.</p></body></html>"
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{ct}},
		Request:    req,
	}, nil
}

// 🔴 THE FENCE (P29 §1.8). A 404 that no platform handler wrote must not be reported as a platform
// refusal.
//
// Verified red before it was verified green: with the `edge404` calls removed from the transports, every
// case below reported the switch's own default or 404 branch — `verdict` said "the platform has no
// proposal … check the id, and that the token belongs to the tenant", which is three wrong
// investigations offered for a deployment gap, and `push-source` said "platform returned 404" followed
// by a slab of HTML.
func TestABare404IsReportedAsNotReachableRatherThanAsARefusal(t *testing.T) {
	const revision = "470cf66b039c73bdd2c21d43094ce41a4db74eae"

	cases := []struct {
		name string
		path string
		call func(*Client) error
	}{
		{"link", runlink.LinkPath, func(c *Client) error {
			_, err := c.Link(context.Background(), runlink.Payload{ContractVersion: runlink.ContractVersion})
			return err
		}},
		{"link --with-ir", runlink.WorkflowIRPath, func(c *Client) error {
			_, err := c.SendWorkflowIR(context.Background(), runlink.WorkflowIRPayload{
				ContractVersion: runlink.WorkflowIRContractVersion, WorkflowID: "w", SourceRevision: revision,
			})
			return err
		}},
		{"push-source", runlink.WorkflowSourcePath, func(c *Client) error {
			_, err := c.PushSource(context.Background(), "w", revision, []byte("bundle"))
			return err
		}},
		{"forget-source", runlink.WorkflowSourcePath, func(c *Client) error {
			return c.DeleteSource(context.Background(), "w", revision)
		}},
		{"discover", runlink.SourceDiscoveryPath, func(c *Client) error {
			_, err := c.RunDiscovery(context.Background(), "w", revision)
			return err
		}},
		{"report-verdict", runlink.VerdictPath, func(c *Client) error {
			_, err := c.ReportVerdict(context.Background(), runlink.VerdictPayload{
				ContractVersion: runlink.VerdictContractVersion, ProposalID: "p", GateResult: "pass",
			})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient("tok", WithRoundTripper(bare404{}))
			err := tc.call(c)
			if err == nil {
				t.Fatalf("a bare 404 produced no error at all")
			}
			msg := err.Error()
			if !strings.Contains(msg, "not reachable at this endpoint") {
				t.Errorf("a bare 404 was reported as a platform answer, not as an unreachable endpoint:\n  %s", msg)
			}
			if !strings.Contains(msg, tc.path) {
				t.Errorf("the message does not name the path an operator has to publish (%s):\n  %s", tc.path, msg)
			}
			// ONE next action, named. `interaction-simplicity-first`: a message that lists three things to
			// try is a message the reader has to triage, and two of the three are known to be wrong here.
			if !strings.Contains(msg, "Next: ") {
				t.Errorf("the message names no next action:\n  %s", msg)
			}
			// The three wrong investigations the old message sent people on, ruled out by name.
			if !strings.Contains(msg, "not a problem with your workflow, your token or your id") {
				t.Errorf("the message does not rule out the causes a reader would check first:\n  %s", msg)
			}
		})
	}
}

// The other direction, and it is the one that keeps the discriminator honest: a 404 a platform handler
// DID write still reports what the platform said. Without this, "not reachable" would swallow every
// legitimate not-found and the CLI would tell people to call an operator about a typo'd id.
func TestAPlatformWritten404StillReportsThePlatformsRefusal(t *testing.T) {
	c := NewClient("tok", WithRoundTripper(bare404{
		contentType: "application/json",
		body:        `{"error":"no such proposal"}`,
	}))
	_, err := c.ReportVerdict(context.Background(), runlink.VerdictPayload{
		ContractVersion: runlink.VerdictContractVersion, ProposalID: "p-404", GateResult: "pass",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "not reachable at this endpoint") {
		t.Fatalf("a platform-written 404 was misreported as an unreachable endpoint:\n  %s", err.Error())
	}
	if !strings.Contains(err.Error(), "has no proposal p-404") {
		t.Fatalf("the platform's own refusal was lost:\n  %s", err.Error())
	}
}
