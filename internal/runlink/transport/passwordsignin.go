package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// passwordsignin.go is the CLI's half of P28 sign-in: an address and a password go out, a PERSONAL
// credential comes back.
//
// # 🔴 What this function must never do with the password
//
// It takes the plaintext, writes it into one JSON body, and drops it. It is never logged, never retried
// against a second host, never put in a URL, and never returned. `PasswordResult` has no field it would fit
// in — which is the same shape `tenancy.Credential` uses to make "accidentally log the secret" impossible
// rather than merely discouraged.
//
// # Why the label is required here and optional on the wire
//
// The platform mints a credential only when a device label is present, which is how one route serves both the
// console (which wants a principal) and the CLI (which wants something durable). So a CLI call with no label
// would authenticate successfully and return nothing usable — a silent no-op. This function refuses to make
// that call.

// ErrBadCredentials is the ONE refusal for an unknown address and a wrong password. The server does not
// distinguish them, deliberately, and neither does this.
var ErrBadCredentials = errors.New("that email and password did not match")

// ErrLocked is the one refusal that IS distinguishable, and the server's own message carries the duration.
// It is wrapped rather than replaced so the CLI can print what the platform said — "try again in 12
// minutes" is information only the server has.
var ErrLocked = errors.New("too many attempts")

// PasswordResult is a completed sign-in. 🔴 It carries the issued credential and never the password.
type PasswordResult struct {
	Token            string
	CredentialID     string
	CredentialKind   string
	Identity         string
	UserID           string
	Email            string
	EmailVerified    bool
	OrganizationID   string
	OrganizationName string
	Role             string
}

// PasswordSignIn authenticates an address and a password and returns a personal credential.
func (c *Client) PasswordSignIn(ctx context.Context, email, plaintext, deviceLabel, organizationID string) (PasswordResult, error) {
	if err := assertLinkTarget(c.base); err != nil {
		return PasswordResult{}, err
	}
	if deviceLabel == "" {
		// See the header: without a label the platform authenticates and mints nothing, so the caller would
		// get a successful-looking response and no credential.
		return PasswordResult{}, errors.New("login: internal: a password sign-in must name this device")
	}
	payload := map[string]string{"email": email, "password": plaintext, "device_label": deviceLabel}
	if organizationID != "" {
		payload["organization_id"] = organizationID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return PasswordResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/auth/password/signin", bytes.NewReader(body))
	if err != nil {
		return PasswordResult{}, err
	}
	// 🔴 No Authorization header. Like the device flow, this is a call made BEFORE the caller has any
	// credential — that is what a sign-in is.
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return PasswordResult{}, fmt.Errorf("login: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var decoded struct {
		TenantID         string `json:"tenant_id"`
		OrganizationID   string `json:"organization_id"`
		OrganizationName string `json:"organization_name"`
		UserID           string `json:"user_id"`
		Email            string `json:"email"`
		EmailVerified    bool   `json:"email_verified"`
		Role             string `json:"role"`
		Credential       struct {
			Token        string `json:"token"`
			CredentialID string `json:"credential_id"`
			Kind         string `json:"kind"`
		} `json:"credential"`
		Error      string `json:"error"`
		ReasonCode string `json:"reason_code"`
	}
	_ = json.Unmarshal(raw, &decoded)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return PasswordResult{}, ErrBadCredentials
	case http.StatusTooManyRequests:
		// The server's own sentence, which carries the remaining duration. Wrapped so a caller can still
		// branch on the kind.
		return PasswordResult{}, fmt.Errorf("%w: %s", ErrLocked, decoded.Error)
	case http.StatusNotFound:
		// 🔴 Named specifically, because this is the failure a customer hits on a deployment whose ingress
		// does not route the auth paths — and "404" on a sign-in reads as "wrong URL" when it means "this
		// install does not offer password sign-in". The two have completely different next actions.
		return PasswordResult{}, errors.New("login: this install does not offer email and password sign-in " +
			"(the platform has no such endpoint) — try `heros login --device`, or ask whoever runs it")
	default:
		if decoded.Error != "" {
			return PasswordResult{}, fmt.Errorf("login: %s", decoded.Error)
		}
		return PasswordResult{}, fmt.Errorf("login: the platform refused the sign-in (%d)", resp.StatusCode)
	}
	if decoded.Credential.Token == "" {
		return PasswordResult{}, errors.New("login: the platform authenticated but issued no credential")
	}

	orgID := decoded.OrganizationID
	if orgID == "" {
		orgID = decoded.TenantID
	}
	identity := decoded.Email
	if identity == "" {
		identity = decoded.UserID
	}
	return PasswordResult{
		Token:            decoded.Credential.Token,
		CredentialID:     decoded.Credential.CredentialID,
		CredentialKind:   decoded.Credential.Kind,
		Identity:         identity,
		UserID:           decoded.UserID,
		Email:            decoded.Email,
		EmailVerified:    decoded.EmailVerified,
		OrganizationID:   orgID,
		OrganizationName: decoded.OrganizationName,
		Role:             decoded.Role,
	}, nil
}
