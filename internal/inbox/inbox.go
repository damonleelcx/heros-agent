package inbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type State string

const (
	StateReceived   State = "received"
	StateVerified   State = "verified"
	StateApplied    State = "applied"
	StateAcked      State = "acked"
	StateRetry      State = "retry"
	StateDeadLetter State = "dead-letter"
)

type Message struct {
	MessageID        string          `json:"message_id"`
	TenantID         string          `json:"tenant_id"`
	PayloadType      string          `json:"payload_type"`
	PayloadVersion   int             `json:"payload_version"`
	Signature        string          `json:"signature"`
	PayloadJSON      json.RawMessage `json:"payload_json"`
	State            State           `json:"state"`
	RetryCount       int             `json:"retry_count"`
	DeadLetterReason string          `json:"dead_letter_reason"`
	CreatedAt        time.Time       `json:"created_at"`
	ExpireAt         *time.Time      `json:"expire_at,omitempty"`
}

func Submit(db *sql.DB, m Message) error {
	if m.MessageID == "" || m.PayloadType == "" || len(m.PayloadJSON) == 0 || m.Signature == "" {
		return fmt.Errorf("missing inbox fields")
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO inbox_messages (message_id, tenant_id, payload_type, payload_version, signature, payload_json, state, retry_count, dead_letter_reason, created_at, expire_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT created_at FROM inbox_messages WHERE message_id = ?), datetime('now')), ?)`,
		m.MessageID, m.TenantID, m.PayloadType, m.PayloadVersion, m.Signature, string(m.PayloadJSON), string(StateReceived), 0, "", m.MessageID, timePtr(m.ExpireAt))
	return err
}

func Transition(db *sql.DB, messageID string, next State, reason string) error {
	_, err := db.Exec(`UPDATE inbox_messages SET state = ?, dead_letter_reason = ?, retry_count = CASE WHEN ? = ? THEN retry_count + 1 ELSE retry_count END WHERE message_id = ?`,
		string(next), reason, string(next), string(StateRetry), messageID)
	return err
}

func Retry(db *sql.DB, messageID, reason string) error {
	return Transition(db, messageID, StateRetry, reason)
}

func DeadLetter(db *sql.DB, messageID, reason string) error {
	return Transition(db, messageID, StateDeadLetter, reason)
}

func VerifySignature(secret string, m Message) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return true
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(m.MessageID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(m.TenantID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(m.PayloadType))
	mac.Write([]byte("\n"))
	mac.Write(m.PayloadJSON)
	expect := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimSpace(m.Signature)), []byte(expect))
}

func List(db *sql.DB, tenantID string) ([]Message, error) {
	rows, err := db.Query(`SELECT message_id, tenant_id, payload_type, payload_version, signature, payload_json, state, retry_count, dead_letter_reason, created_at, expire_at FROM inbox_messages WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var payload string
		var created, expire sql.NullString
		if err := rows.Scan(&m.MessageID, &m.TenantID, &m.PayloadType, &m.PayloadVersion, &m.Signature, &payload, &m.State, &m.RetryCount, &m.DeadLetterReason, &created, &expire); err != nil {
			return nil, err
		}
		m.PayloadJSON = []byte(payload)
		out = append(out, m)
	}
	return out, rows.Err()
}

func Get(db *sql.DB, messageID string) (*Message, error) {
	row := db.QueryRow(`SELECT message_id, tenant_id, payload_type, payload_version, signature, payload_json, state, retry_count, dead_letter_reason, created_at, expire_at FROM inbox_messages WHERE message_id = ?`, messageID)
	var m Message
	var payload string
	var created, expire sql.NullString
	if err := row.Scan(&m.MessageID, &m.TenantID, &m.PayloadType, &m.PayloadVersion, &m.Signature, &payload, &m.State, &m.RetryCount, &m.DeadLetterReason, &created, &expire); err != nil {
		return nil, err
	}
	m.PayloadJSON = []byte(payload)
	return &m, nil
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
