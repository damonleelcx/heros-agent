package inbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/db"
)

func TestInboxSubmitTransitionAndGet(t *testing.T) {
	database, err := db.Open(t.TempDir() + string(rune('\\')) + "inbox.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	msg := Message{
		MessageID:      "msg-1",
		TenantID:       "tenant-a",
		PayloadType:    "skill",
		PayloadVersion: 1,
		PayloadJSON:    json.RawMessage(`{"name":"skill-a"}`),
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(msg.MessageID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(msg.TenantID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(msg.PayloadType))
	mac.Write([]byte("\n"))
	mac.Write(msg.PayloadJSON)
	msg.Signature = hex.EncodeToString(mac.Sum(nil))

	if !VerifySignature("secret", msg) {
		t.Fatalf("expected signature to verify")
	}
	if err := Submit(database, msg); err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, err := Get(database, msg.MessageID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != StateReceived {
		t.Fatalf("expected received, got %s", got.State)
	}
	if err := Transition(database, msg.MessageID, StateVerified, "ok"); err != nil {
		t.Fatalf("transition verify: %v", err)
	}
	got, err = Get(database, msg.MessageID)
	if err != nil {
		t.Fatalf("get after transition: %v", err)
	}
	if got.State != StateVerified {
		t.Fatalf("expected verified, got %s", got.State)
	}
	if err := Retry(database, msg.MessageID, "retry"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, err = Get(database, msg.MessageID)
	if err != nil {
		t.Fatalf("get after retry: %v", err)
	}
	if got.State != StateRetry {
		t.Fatalf("expected retry, got %s", got.State)
	}
	if got.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", got.RetryCount)
	}
}
