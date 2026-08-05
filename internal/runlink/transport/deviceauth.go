package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// deviceauth.go is the CLI's half of device authorization (P27 task 13.2).
//
// # What the CLI holds, and what it never sees
//
// It holds a device code — 32 random bytes the server minted — and nothing else. It never sees a
// password, an assertion or an ID token, and it never renders one: the person authenticates in a browser
// against their own identity provider, and the only thing that crosses back into the terminal is a
// credential the platform issued.
//
// That is the point of the whole flow rather than a nicety. A terminal that could accept an assertion
// would be a terminal somebody could be talked into pasting one into.
//
// # 🔴 The polling contract, and why both bounds are the CLI's job
//
// The server tells the CLI an interval and an expiry, and the CLI obeys both — but it also imposes its
// own ceiling, because a server that returned `interval: 0` and `expires_in: 86400` would otherwise
// produce a terminal that spins a CPU for a day. A client that trusts an upstream's pacing without a
// floor is a client that can be told to attack the thing it is polling.

// ErrDeviceCode is the ONE refusal a poll can produce: denied, expired, already used, or never existed.
// The server does not distinguish them and neither does this.
var ErrDeviceCode = errors.New("that device code is no longer usable")

// DeviceAuth is what the server returns when a code is minted. Both values are plaintext and neither is
// readable again — the user code goes on the terminal, the device code stays in memory.
type DeviceAuth struct {
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	VerificationURI string `json:"verification_uri"`
	IntervalSeconds int    `json:"interval_seconds"`
	ExpiresSeconds  int    `json:"expires_in_seconds"`
}

// DeviceResult is a completed authorization: the credential and who it belongs to.
type DeviceResult struct {
	Token            string `json:"token"`
	Identity         string `json:"identity"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	CredentialID     string `json:"credential_id"`
}

// devicePollFloor and devicePollCeiling bound whatever the server asks for. A server that said "poll
// every 0s" would otherwise get a hot loop; one that said "every 5 minutes" would make a login feel
// broken.
const (
	devicePollFloor   = 1 * time.Second
	devicePollCeiling = 10 * time.Second
	// deviceWaitCeiling is the CLI's own total bound, applied even if the server claims a longer expiry.
	// A terminal that waits forever is a terminal somebody leaves open.
	deviceWaitCeiling = 15 * time.Minute
)

// RequestDeviceAuth asks for a code. `label` describes this machine and is a DISPLAY string — the server
// never compares it, so nothing here binds the code to anything the client chose.
func (c *Client) RequestDeviceAuth(ctx context.Context, label string) (DeviceAuth, error) {
	if err := assertLinkTarget(c.base); err != nil {
		return DeviceAuth{}, err
	}
	body, _ := json.Marshal(map[string]string{"label": label})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/device/authorize", bytes.NewReader(body))
	if err != nil {
		return DeviceAuth{}, err
	}
	// 🔴 No Authorization header, and that is the whole point: the CLI has no credential yet. This is the
	// one platform call in the product that carries none.
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("login: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return DeviceAuth{}, fmt.Errorf("login: the platform would not start a device login (%d)", resp.StatusCode)
	}
	var out DeviceAuth
	if err := json.Unmarshal(raw, &out); err != nil {
		return DeviceAuth{}, fmt.Errorf("login: decode response: %w", err)
	}
	if out.UserCode == "" || out.DeviceCode == "" {
		return DeviceAuth{}, fmt.Errorf("login: the platform returned an incomplete device authorization")
	}
	return out, nil
}

// PollDeviceAuth waits for a person to approve, then returns the issued credential.
//
// It returns ErrDeviceCode for every terminal refusal and `context.DeadlineExceeded` when the wait runs
// out — two outcomes, because the user's next action differs: start over, or the approval never came.
func (c *Client) PollDeviceAuth(ctx context.Context, d DeviceAuth, onWait func()) (DeviceResult, error) {
	interval := time.Duration(d.IntervalSeconds) * time.Second
	if interval < devicePollFloor {
		interval = devicePollFloor
	}
	if interval > devicePollCeiling {
		interval = devicePollCeiling
	}
	wait := time.Duration(d.ExpiresSeconds) * time.Second
	if wait <= 0 || wait > deviceWaitCeiling {
		wait = deviceWaitCeiling
	}
	deadline := time.Now().Add(wait)

	for {
		res, err := c.pollOnce(ctx, d.DeviceCode)
		switch {
		case err == nil:
			return res, nil
		case errors.Is(err, errDevicePending):
			// keep waiting
		default:
			return DeviceResult{}, err
		}
		if time.Now().Add(interval).After(deadline) {
			return DeviceResult{}, context.DeadlineExceeded
		}
		if onWait != nil {
			onWait()
		}
		select {
		case <-ctx.Done():
			return DeviceResult{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// errDevicePending is internal: "not yet" is not a failure and must never reach a caller as one.
var errDevicePending = errors.New("device authorization is still pending")

func (c *Client) pollOnce(ctx context.Context, deviceCode string) (DeviceResult, error) {
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/device/token", bytes.NewReader(body))
	if err != nil {
		return DeviceResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return DeviceResult{}, fmt.Errorf("login: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// Every non-200 is the same refusal. The server already refuses to distinguish denial from expiry
		// from unknown; a client that inferred a difference from a status code would rebuild the oracle
		// the server was careful not to be.
		return DeviceResult{}, ErrDeviceCode
	}
	var out struct {
		Status string `json:"status"`
		DeviceResult
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return DeviceResult{}, fmt.Errorf("login: decode response: %w", err)
	}
	if out.Status == "pending" {
		return DeviceResult{}, errDevicePending
	}
	if out.Token == "" || out.Identity == "" {
		return DeviceResult{}, ErrDeviceCode
	}
	return out.DeviceResult, nil
}

// Identity is `whoami`, read for everything it now says (task 13.5).
//
// 🔴 `Validate` is UNTOUCHED and still returns only the identity string. Two callers depend on it — the
// CLI's token validation and the console's platform-token seam — and P27's rule for that endpoint is
// that `identity` keeps its name, meaning and value. This is a SECOND reader beside it rather than a
// change to it: a caller that wants who and where asks for who and where, and a caller that wants what
// it always got is not made to care.
type Identity struct {
	Identity         string `json:"identity"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	UserID           string `json:"user_id"`
	CredentialKind   string `json:"credential_kind"`
}

// WhoAmI reads the full identity document.
func (c *Client) WhoAmI(ctx context.Context) (Identity, error) {
	if err := assertLinkTarget(c.base); err != nil {
		return Identity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/whoami", nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("login: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return Identity{}, fmt.Errorf("login: the platform rejected this token")
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("login: platform returned %d", resp.StatusCode)
	}
	var out Identity
	if err := json.Unmarshal(raw, &out); err != nil {
		return Identity{}, fmt.Errorf("login: decode response: %w", err)
	}
	if out.Identity == "" {
		return Identity{}, fmt.Errorf("login: platform accepted the token but returned no identity")
	}
	return out, nil
}
