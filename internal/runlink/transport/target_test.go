package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// This file exists because of a shipped break, and its shape is the lesson.
//
// assertLinkTarget used to compare the WHOLE request URL against PlatformBaseURL+LinkPath, so every
// transport that addresses any other path — push-source, the workflow-IR upload, platform-side
// discovery, report-verdict — refused its own request with "refusing to transmit … is not the pinned
// link endpoint". Four shipped commands could not transmit at all.
//
// The pin's own test passed throughout, because it enumerated the URLs the PIN allowed and asserted the
// pin allowed them. That is a tautology: it can only fail if someone edits both halves of one file. The
// test below instead drives the REAL transport methods and asserts a request left, so a path the guard
// does not know about is a failure NAMED BY THE METHOD that uses it. A new transport that forgets to
// declare its path fails here rather than in a customer's terminal.

// recorder is a RoundTripper that records the request URL and answers with a canned 200. The guard runs
// BEFORE the transport, so a refused request never reaches this — which is exactly the signal we want.
type recorder struct {
	urls []string
	body string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.URL.String())
	body := r.body
	if body == "" {
		body = "{}"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

func TestEveryPlatformPathIsALinkTarget(t *testing.T) {
	const (
		workflowID = "workflow"
		revision   = "470cf66b039c73bdd2c21d43094ce41a4db74eae"
	)

	cases := []struct {
		name string
		// body is what the platform answers; a few methods refuse an empty identity/id.
		body string
		call func(*Client) error
	}{
		{
			name: "login/Validate",
			body: `{"identity":"heros"}`,
			call: func(c *Client) error {
				_, err := c.Validate(context.Background())
				return err
			},
		},
		{
			name: "link/Link",
			call: func(c *Client) error {
				_, err := c.Link(context.Background(), runlink.Payload{ContractVersion: runlink.ContractVersion})
				return err
			},
		},
		{
			name: "link --with-ir/SendWorkflowIR",
			call: func(c *Client) error {
				_, err := c.SendWorkflowIR(context.Background(), runlink.WorkflowIRPayload{
					ContractVersion: runlink.WorkflowIRContractVersion,
					WorkflowID:      workflowID,
					SourceRevision:  revision,
				})
				return err
			},
		},
		{
			name: "push-source/PushSource",
			call: func(c *Client) error {
				_, err := c.PushSource(context.Background(), workflowID, revision, []byte("bundle"))
				return err
			},
		},
		{
			name: "push-source --forget/DeleteSource",
			call: func(c *Client) error {
				return c.DeleteSource(context.Background(), workflowID, revision)
			},
		},
		{
			name: "push-source discovery/RunDiscovery",
			call: func(c *Client) error {
				_, err := c.RunDiscovery(context.Background(), workflowID, revision)
				return err
			},
		},
		{
			name: "report-verdict/ReportVerdict",
			call: func(c *Client) error {
				_, err := c.ReportVerdict(context.Background(), runlink.VerdictPayload{
					ContractVersion: runlink.VerdictContractVersion,
					ProposalID:      "prop-1",
				})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{body: tc.body}
			c := NewClient("token", WithRoundTripper(rec))

			err := tc.call(c)
			if err != nil && strings.Contains(err.Error(), "refusing to transmit") {
				t.Fatalf("%s was refused by its own egress pin: %v\n"+
					"the path it addresses is not in runlink.PlatformPaths", tc.name, err)
			}
			// Any other error is the platform's answer being unconvincing to this canned body; the
			// subject here is only whether the request was allowed OUT.
			if len(rec.urls) == 0 {
				t.Fatalf("%s sent no request at all (err=%v)", tc.name, err)
			}
			for _, u := range rec.urls {
				if !runlink.IsLinkTarget(u) {
					t.Errorf("%s addressed %q, which IsLinkTarget refuses", tc.name, u)
				}
				if !strings.HasPrefix(u, runlink.PlatformBaseURL) {
					t.Errorf("%s addressed %q, which is not under %s", tc.name, u, runlink.PlatformBaseURL)
				}
			}
		})
	}
}

// TestLinkTargetRefusesOffOrigin keeps the widening honest: broadening the pin from one URL to one
// ORIGIN must not broaden it to one HOST, one scheme, or one anything-with-a-query-string.
func TestLinkTargetRefusesOffOrigin(t *testing.T) {
	bad := []string{
		"http://heros-agent.space/api/v1/run-links",           // wrong scheme
		"https://heros-agent.space:8443/api/v1/run-links",     // wrong port
		"https://staging.heros-agent.space/api/v1/run-links",  // subdomain
		"https://heros-agent.space.evil/api/v1/run-links",     // suffix attack
		"https://evil.example/api/v1/run-links",               // wrong host
		"https://user:pw@heros-agent.space/api/v1/run-links",  // credentials in the URL
		"https://heros-agent.space/api/v1/run-links?to=evil",  // query string
		"https://heros-agent.space/api/v1/run-links#evil",     // fragment
		"https://heros-agent.space/api/v1/admin/tenants",      // an unlisted path on the right origin
		"https://heros-agent.space/../etc/passwd",             // traversal off the declared paths
		"https://heros-agent.space/api/v1/workflows/../admin", // traversal out of a prefix entry
		"",
	}
	for _, u := range bad {
		if runlink.IsLinkTarget(u) {
			t.Errorf("IsLinkTarget(%q) = true, want false", u)
		}
	}
}
