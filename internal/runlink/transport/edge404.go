package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// edge404.go answers one question the CLI could not previously answer, and which cost a customer a
// console with nothing in it: **did that 404 come from the platform, or from the thing in front of it?**
//
// # Why the two are not the same failure, at all
//
// A 404 from a platform handler means the platform is there, it authenticated the caller, and it is
// telling them the subject they named does not exist for them. The next action is to check the id.
//
// A 404 from the EDGE means the request never reached a platform handler. The reverse proxy has no rule
// for that path, so it fell through to the console's Next.js app, which rendered its own not-found page.
// Nothing was authenticated and nothing was refused — the endpoint is simply not published. The next
// action is an operator's, not the caller's, and no amount of checking the id will help.
//
// 🔴 They looked IDENTICAL from the CLI. `heros link --with-ir` against production printed "the platform
// has no such workflow", which is a sentence about the customer's data, on a day when the truth was that
// `/api/v1/workflows/{id}/ir` was not in the ingress manifest at all. A developer reading that message
// checks their workflow id, then their token, then their tenant — three investigations of things that
// were never wrong. This is the discriminator that makes the fourth thing, the true one, the first thing
// they are told.
//
// # How the two are told apart
//
// A platform handler answers through `writeJSON`, which sets `Content-Type: application/json` and writes
// a JSON object. Next.js's not-found page is `text/html`; a bare proxy 404 is `text/plain` or nothing.
// So: a response is FROM A PLATFORM HANDLER when it declares JSON and its body parses as a JSON object.
//
// This errs toward silence rather than toward a wrong claim. A platform handler that someday answers 404
// with an empty body would be classified as an edge failure and the message would name an operator
// action that is not needed — annoying. The reverse (an unpublished path reported as "no such
// workflow") is the failure that already happened and cost three wrong investigations, and it is the one
// worth erring away from.

// fromPlatformHandler reports whether a response was written by a platform handler rather than by
// whatever proxy or app sits in front of it.
func fromPlatformHandler(resp *http.Response, body []byte) bool {
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return false
	}
	var probe map[string]json.RawMessage
	return json.Unmarshal(body, &probe) == nil
}

// notReachableAtEndpoint builds the message for a 404 that did not come from a platform handler.
//
// command names the CLI command, so the sentence reads as the command's own report. path is the path
// that was addressed, because the operator's remedy is an ingress rule for exactly that string — naming
// it turns a support conversation into a one-line diff.
//
// ONE next action, per `interaction-simplicity-first`: when a prerequisite is missing the message
// contains the step, and a list of three possible steps is a list the reader has to triage.
func notReachableAtEndpoint(command, endpoint, path string) error {
	return fmt.Errorf(
		"%s: %s is not reachable at this endpoint — the request reached %s but no platform handler "+
			"answered it, so the path is not published there. Nothing was transmitted and nothing was "+
			"refused: this is a deployment gap, not a problem with your workflow, your token or your id. "+
			"Next: ask whoever operates %s to publish %s (an Exact ingress rule to agentd:4321); "+
			"`deploy/k8s/overlays/prod/ingress.yaml` carries the others",
		command, path, endpoint, endpoint, path)
}

// edge404 returns the not-reachable error when resp is a 404 that no platform handler wrote, and nil
// otherwise — so a caller reads it as "did the edge eat this?" and falls through to its own handling.
func (c *Client) edge404(command, path string, resp *http.Response, body []byte) error {
	if resp.StatusCode != http.StatusNotFound || fromPlatformHandler(resp, body) {
		return nil
	}
	return notReachableAtEndpoint(command, c.base, path)
}
