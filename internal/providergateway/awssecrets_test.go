package providergateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// What these tests do and do not prove — read this before trusting them.
//
// The client under test is the REAL secretsmanager client (secretsmanager.NewFromConfig), signing
// real SigV4, serialising real AWS JSON 1.1, and parsing real responses. Nothing here is a fake
// implementation of AWSSecretsManager or a stub that returns canned Credentials — that would be the
// mock this project forbids, and it would prove only that the test's own fiction is self-consistent.
//
// What IS substituted is the endpoint: cfg.BaseEndpoint points at an httptest server that replays
// the wire shapes AWS documents for GetSecretValue. So the request bytes are real, the response
// bytes are real, and the protocol handling is the SDK's. The seam is the network, which is the
// correct seam and the only one available without an AWS account.
//
// !!! What this therefore does NOT prove: that a real IAM policy grants GetSecretValue, that a real
// ARN resolves, that a real KMS key decrypts, or that the endpoint's TLS is right. Those need live
// credentials and are stated as unverified in the report rather than implied to be covered.

// awsJSONTarget is the header the AWS JSON 1.1 protocol dispatches on. Asserting it is how these
// tests know the SDK really serialised a GetSecretValue call rather than the server being asked
// anything at all.
const awsJSONTarget = "secretsmanager.GetSecretValue"

// smServer is an httptest server replaying Secrets Manager's documented wire shapes.
//
// respond receives the SecretId the SDK actually sent, so a test can assert the request as well as
// stub the response.
type smServer struct {
	*httptest.Server
	calls atomic.Int64
	// lastAuth is the Authorization header of the most recent request — proof the real signer ran.
	lastAuth atomic.Value
}

func newSMServer(t *testing.T, respond func(secretID string) (int, string)) *smServer {
	t.Helper()
	s := &smServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.lastAuth.Store(r.Header.Get("Authorization"))

		if got := r.Header.Get("X-Amz-Target"); got != awsJSONTarget {
			t.Errorf("X-Amz-Target = %q, want %q — the SDK did not serialise a GetSecretValue call", got, awsJSONTarget)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-amz-json-1.1") {
			t.Errorf("Content-Type = %q, want AWS JSON 1.1", ct)
		}
		body, _ := io.ReadAll(r.Body)
		var in struct{ SecretId string }
		if err := json.Unmarshal(body, &in); err != nil {
			t.Errorf("request body is not AWS JSON 1.1: %v", err)
		}

		code, payload := respond(in.SecretId)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(s.Close)
	return s
}

// okSecret is Secrets Manager's documented success shape for GetSecretValue.
func okSecret(arn, secretString string) string {
	b, _ := json.Marshal(map[string]any{
		"ARN":           arn,
		"Name":          "provider-key",
		"VersionId":     "01234567-89ab-cdef-0123-456789abcdef",
		"SecretString":  secretString,
		"VersionStages": []string{"AWSCURRENT"},
		"CreatedDate":   1.523477145713e9,
	})
	return string(b)
}

// awsErr is Secrets Manager's documented error shape.
func awsErr(typ, msg string) string {
	b, _ := json.Marshal(map[string]string{"__type": typ, "message": msg})
	return string(b)
}

func testAWSConfig(endpoint string) aws.Config {
	return aws.Config{
		Region: "us-east-1",
		// Static creds so the real signer has something to sign with. These are the SDK's own
		// credential type — the signing path is production's.
		Credentials:  credentials.NewStaticCredentialsProvider("AKIAEXAMPLE", "not-a-real-aws-secret", ""),
		BaseEndpoint: aws.String(endpoint),
	}
}

func TestAWSSecrets_BearerKeyIsFetchedThroughTheRealClient(t *testing.T) {
	const theKey = "sk-ant-api03-REAL-LOOKING-KEY-abcdef123456"
	var gotID string
	srv := newSMServer(t, func(id string) (int, string) {
		gotID = id
		return 200, okSecret("arn:aws:secretsmanager:us-east-1:1:secret:heros/anthropic", `{"api_key":"`+theKey+`"}`)
	})

	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL),
		map[string]string{ProviderAnthropic: "heros/providers/anthropic"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}

	cred, err := s.Credential(context.Background(), ProviderAnthropic)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.APIKey != theKey {
		t.Errorf("APIKey = %q, want %q", cred.APIKey, theKey)
	}
	if gotID != "heros/providers/anthropic" {
		t.Errorf("SecretId sent = %q, want the configured mapping", gotID)
	}
	// The request was really signed. Without this the test would pass against a client that skipped
	// authentication entirely, which is precisely the difference between a real integration and a
	// fake one.
	auth, _ := srv.lastAuth.Load().(string)
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization = %q, want a real SigV4 signature", auth)
	}
}

// Parity with EnvSecrets: it serves all three providers, so a "real" manager that only served the
// bearer two would be an adoption blocker, not an upgrade.
func TestAWSSecrets_AWSShapedPayloadServesBedrock(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) {
		return 200, okSecret("arn:aws:secretsmanager:us-east-1:1:secret:heros/bedrock",
			`{"access_key_id":"AKIAROTATED","secret_access_key":"bedrock-secret-value-xyz","session_token":"tok-123","region":"eu-west-1"}`)
	})
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderBedrock: "heros/providers/bedrock"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	cred, err := s.Credential(context.Background(), ProviderBedrock)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.AWS == nil {
		t.Fatal("AWS credential is nil; the bedrock adapter would reject this")
	}
	if cred.AWS.AccessKeyID != "AKIAROTATED" || cred.AWS.SecretAccessKey != "bedrock-secret-value-xyz" ||
		cred.AWS.SessionToken != "tok-123" || cred.Region != "eu-west-1" {
		t.Errorf("credential did not round-trip: %+v", cred.AWS)
	}
	// The value round-trips into the scrubber's list, which is what makes it redactable downstream.
	if got := cred.secretValues(); len(got) != 2 {
		t.Errorf("secretValues() = %v, want the secret access key and the session token", got)
	}
}

// The L1 test. parseSecretPayload is the one place that holds a plaintext secret alongside an error
// path, so a %q of the payload there would put a live credential into a caller's log — and the
// gateway's scrubber cannot save it, because the scrubber only knows the secrets of a credential that
// PARSED, and these are the paths where parsing failed.
//
// Honest scope: this does not catch a leak that exists today. It was written expecting encoding/json
// to quote the offending input in its errors; it does not (see parseSecretPayload's note), so wrapping
// the unmarshal error would leak at most one character. This is a REGRESSION guard — it fails the
// moment someone adds the payload to an error message here, which was verified by doing exactly that
// and watching it go red. A guard that has never been seen to fail is a guard nobody should trust.
func TestAWSSecrets_AMalformedPayloadNeverAppearsInTheError(t *testing.T) {
	const leaky = "sk-ant-THIS-MUST-NOT-APPEAR-IN-AN-ERROR-9f8e7d"

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"malformed json carrying the key", `{"api_key":"` + leaky + `"`},
		{"json but wrong field name", `{"apikey":"` + leaky + `"}`},
		{"bare string, not an object", `"` + leaky + `"`},
		{"half an aws credential", `{"access_key_id":"AKIA1","secret_access_key":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newSMServer(t, func(string) (int, string) {
				return 200, okSecret("arn:aws:secretsmanager:us-east-1:1:secret:x", tc.payload)
			})
			s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: "heros/openai"})
			if err != nil {
				t.Fatalf("NewAWSSecretsManager: %v", err)
			}
			_, err = s.Credential(context.Background(), ProviderOpenAI)
			if err == nil {
				t.Fatal("a malformed payload was accepted; it would be sent to the provider as a credential")
			}
			if !errors.Is(err, ErrSecretMalformed) {
				t.Errorf("want ErrSecretMalformed (the fix is the payload, not IAM), got %v", err)
			}
			if strings.Contains(err.Error(), leaky) {
				t.Fatalf("THE SECRET IS IN THE ERROR: %v", err)
			}
			// The error must still be actionable: it names the secret and the provider.
			if !strings.Contains(err.Error(), "heros/openai") {
				t.Errorf("error does not name the secret, so nobody knows what to fix: %v", err)
			}
		})
	}
}

// The control for the test above: it must be capable of FAILING. If parseSecretPayload's errors were
// tested only against payloads that never contained the sentinel, "no leak" would be vacuous.
func TestAWSSecrets_TheLeakAssertionActuallyDetectsALeak(t *testing.T) {
	const leaky = "sk-ant-THIS-MUST-NOT-APPEAR-IN-AN-ERROR-9f8e7d"
	// A deliberately leaky formatting of the same failure, standing in for the regression this guards.
	leaked := fmt.Errorf("%w: secret payload %q is not valid JSON", ErrSecretMalformed, `{"api_key":"`+leaky+`"`)
	if !strings.Contains(leaked.Error(), leaky) {
		t.Fatal("the assertion used above cannot detect a secret in an error message; " +
			"TestAWSSecrets_AMalformedPayloadNeverAppearsInTheError proves nothing")
	}
}

func TestAWSSecrets_FetchFailureFailsClosedAndNamesTheSecret(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) {
		return 400, awsErr("AccessDeniedException", "User is not authorized to perform secretsmanager:GetSecretValue")
	})
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: "heros/providers/openai"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	_, err = s.Credential(context.Background(), ProviderOpenAI)
	if err == nil {
		t.Fatal("a denied fetch returned no error; the gateway would call the provider with an empty key")
	}
	if !errors.Is(err, ErrNoCredential) {
		t.Errorf("want ErrNoCredential, got %v", err)
	}
	// The actionable part of an IAM failure is the exception name. Losing it turns a two-minute fix
	// into an investigation.
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error dropped the AWS reason: %v", err)
	}
	if !strings.Contains(err.Error(), "heros/providers/openai") {
		t.Errorf("error does not name the secret: %v", err)
	}
}

// There is no env fallback, and that is load-bearing: a manager that silently degraded to an
// environment variable would keep serving calls while /readyz still claimed AWS, and the deployment
// would be lying about its own security posture.
func TestAWSSecrets_DoesNotFallBackToTheEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-fallback-would-be-used-here")
	srv := newSMServer(t, func(string) (int, string) {
		return 400, awsErr("ResourceNotFoundException", "Secrets Manager can't find the specified secret.")
	})
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: "heros/openai"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	cred, err := s.Credential(context.Background(), ProviderOpenAI)
	if err == nil {
		t.Fatal("the AWS source fell back to something; it must fail closed")
	}
	if cred.APIKey != "" {
		t.Fatalf("a credential was returned despite the failure: %q", cred.APIKey)
	}
}

func TestAWSSecrets_UnmappedProviderFailsClosedWithoutCallingAWS(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) {
		return 200, okSecret("arn:x", `{"api_key":"sk-should-never-be-fetched"}`)
	})
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: "heros/openai"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	if _, err := s.Credential(context.Background(), ProviderAnthropic); !errors.Is(err, ErrNoCredential) {
		t.Errorf("want ErrNoCredential for an unmapped provider, got %v", err)
	}
	if n := srv.calls.Load(); n != 0 {
		t.Errorf("called AWS %d times for a provider it has no mapping for; want 0", n)
	}
}

// The cache is the difference between one API call and one per model call. Asserting the call COUNT
// rather than the returned value is the point: a cache that refetched every time would still return
// the right credential, and the test would pass while the bill and the throttle ceiling did not.
func TestAWSSecrets_CachesWithinTTLAndRefetchesAfterIt(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) {
		return 200, okSecret("arn:x", `{"api_key":"sk-cached-value-abcdef"}`)
	})
	now := time.Now()
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL),
		map[string]string{ProviderOpenAI: "heros/openai"},
		WithSecretTTL(5*time.Minute),
		withSecretClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.Credential(ctx, ProviderOpenAI); err != nil {
			t.Fatalf("Credential #%d: %v", i, err)
		}
	}
	if n := srv.calls.Load(); n != 1 {
		t.Errorf("fetched %d times within the TTL; want 1 (every model call would hit AWS)", n)
	}

	// Past the TTL a rotated secret must be picked up — the bounded staleness this design promises.
	now = now.Add(5*time.Minute + time.Second)
	if _, err := s.Credential(ctx, ProviderOpenAI); err != nil {
		t.Fatalf("Credential after TTL: %v", err)
	}
	if n := srv.calls.Load(); n != 2 {
		t.Errorf("fetched %d times total; want 2 — a rotated secret would never be picked up", n)
	}
}

func TestAWSSecrets_ZeroTTLFetchesEveryTime(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) {
		return 200, okSecret("arn:x", `{"api_key":"sk-uncached-value-abcdef"}`)
	})
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL),
		map[string]string{ProviderOpenAI: "heros/openai"}, WithSecretTTL(0))
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Credential(context.Background(), ProviderOpenAI); err != nil {
			t.Fatalf("Credential #%d: %v", i, err)
		}
	}
	if n := srv.calls.Load(); n != 3 {
		t.Errorf("fetched %d times with caching disabled; want 3", n)
	}
}

// /readyz is unauthenticated and its body lands in monitoring systems. It must name the door, never
// what is behind it, and never enough to go looking.
func TestAWSSecrets_DescribeLeaksNeitherSecretsNorSecretIDs(t *testing.T) {
	srv := newSMServer(t, func(string) (int, string) { return 200, okSecret("arn:x", `{"api_key":"sk-x"}`) })
	const arn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:heros/prod/openai-AbCdEf"
	s, err := NewAWSSecretsManager(testAWSConfig(srv.URL), map[string]string{ProviderOpenAI: arn})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}
	info := s.Describe()
	if info.Kind != SourceKindAWSSecretsManager {
		t.Errorf("Kind = %q, want %q", info.Kind, SourceKindAWSSecretsManager)
	}
	if strings.Contains(info.Detail, arn) || strings.Contains(info.Detail, "123456789012") {
		t.Errorf("Describe exposes the secret's ARN/account on an unauthenticated endpoint: %q", info.Detail)
	}
	// It must still be USEFUL — an operator needs to know which providers this source can serve.
	if !strings.Contains(info.Detail, ProviderOpenAI) || !strings.Contains(info.Detail, "us-east-1") {
		t.Errorf("Describe is not actionable: %q", info.Detail)
	}
}

func TestAWSSecrets_ConstructorRejectsAnEmptyMapping(t *testing.T) {
	if _, err := NewAWSSecretsManager(testAWSConfig("http://127.0.0.1:1"), nil); err == nil {
		t.Error("a secrets source with nothing to fetch was accepted; it would 401 at the provider later")
	}
	if _, err := NewAWSSecretsManager(testAWSConfig("http://127.0.0.1:1"),
		map[string]string{ProviderOpenAI: ""}); err == nil {
		t.Error("an empty secret ID was accepted")
	}
}

// The end-to-end statement task 4.5 actually asks for: a credential that came from the secrets
// manager is still scrubbed out of everything the gateway emits. Without this, the new source could
// be a hole in a guarantee the old source honoured.
func TestAWSSecrets_ASecretFromTheManagerIsStillScrubbedFromGatewayErrors(t *testing.T) {
	const theKey = "sk-from-secrets-manager-DO-NOT-LEAK-4f2a9c"
	sm := newSMServer(t, func(string) (int, string) {
		return 200, okSecret("arn:x", `{"api_key":"`+theKey+`"}`)
	})
	secrets, err := NewAWSSecretsManager(testAWSConfig(sm.URL), map[string]string{ProviderOpenAI: "heros/openai"})
	if err != nil {
		t.Fatalf("NewAWSSecretsManager: %v", err)
	}

	// A provider that echoes the key back in its error — the realistic leak path.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided: `+theKey+`"}}`)
	}))
	defer provider.Close()

	g := New(secrets, WithBaseURL(ProviderOpenAI, provider.URL), WithMaxRetries(0))
	_, err = g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"),
		Request{Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err == nil {
		t.Fatal("want an error from a 401")
	}
	if strings.Contains(err.Error(), theKey) {
		t.Fatalf("a credential sourced from AWS Secrets Manager leaked into a gateway error: %v", err)
	}
	if !strings.Contains(err.Error(), redacted) {
		t.Errorf("the key was neither present nor redacted; is the scrubber seeing this source? %v", err)
	}
}
