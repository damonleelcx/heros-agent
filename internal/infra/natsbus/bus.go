package natsbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/nats-io/nats.go"
)

// Bus publishes enterprise events (proposal, memory, fleet). Uses JetStream when JS is non-nil.
type Bus struct {
	Conn   *nats.Conn
	JS     nats.JetStreamContext
	NodeID string
}

func Connect(url, nodeID string) (*Bus, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("empty nats url")
	}
	nc, err := nats.Connect(url,
		nats.Name("heros-agentd"),
		nats.Timeout(10*time.Second),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return &Bus{Conn: nc, NodeID: nodeID}, nil
}

// InitJetStream ensures a durable stream over heros.> (requires NATS -js).
func (b *Bus) InitJetStream(streamName string, maxAge time.Duration) error {
	if b == nil || b.Conn == nil {
		return fmt.Errorf("no nats connection")
	}
	js, err := b.Conn.JetStream()
	if err != nil {
		return err
	}
	cfg := &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"heros.>"},
		Retention: nats.LimitsPolicy,
		MaxAge:    maxAge,
		Storage:   nats.FileStorage,
	}
	if _, err := js.StreamInfo(streamName); err != nil {
		if _, err := js.AddStream(cfg); err != nil {
			return fmt.Errorf("jetstream add stream: %w", err)
		}
	}
	b.JS = js
	return nil
}

func (b *Bus) Close() {
	if b != nil && b.Conn != nil {
		b.Conn.Close()
	}
}

func (b *Bus) subjectLocal(suffix string) string {
	return fmt.Sprintf("heros.node.%s.%s", sanitize(b.NodeID), suffix)
}

func (b *Bus) subjectFleet(suffix string) string {
	return "heros.fleet." + suffix
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ".", "_")
	if s == "" {
		return "unknown"
	}
	return s
}

func (b *Bus) publish(subject string, data []byte) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	if b.JS != nil {
		if _, err := b.JS.Publish(subject, data); err != nil {
			return err
		}
		return nil
	}
	return b.Conn.Publish(subject, data)
}

// PublishProposalSubmitted notifies fleet bus that a mutation awaits human approval.
func (b *Bus) PublishProposalSubmitted(p approval.Proposal) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := b.publish(b.subjectFleet("proposals.pending"), payload); err != nil {
		return err
	}
	return b.publish(b.subjectLocal("proposals.pending"), payload)
}

// PublishProposalApproved after human sign-off.
func (b *Bus) PublishProposalApproved(p approval.Proposal) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return b.publish(b.subjectFleet("proposals.approved"), payload)
}

// PublishMemoryPromoted when episodic → vector index.
func (b *Bus) PublishMemoryPromoted(sessionID, pointID, text string, importance float64) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	m := map[string]any{
		"session_id": sessionID,
		"point_id":   pointID,
		"text":       text,
		"importance": importance,
		"node_id":    b.NodeID,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.publish(b.subjectFleet("memory.promoted"), payload)
}

// PublishJobFired marks a scheduled job execution (observability / downstream triggers).
func (b *Bus) PublishJobFired(jobID, tenantID, action string, payload []byte) error {
	if b == nil || b.Conn == nil {
		return nil
	}
	m := map[string]any{
		"job_id": jobID, "tenant_id": tenantID, "action": action,
		"ts":     time.Now().UTC().Format(time.RFC3339),
	}
	if len(payload) > 0 {
		m["payload"] = json.RawMessage(payload)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.publish(b.subjectFleet("scheduler.fired"), data)
}

// SubscribeFleet subscribes to fleet events (JetStream-aware).
func (b *Bus) SubscribeFleet(pattern string, handler func(subject string, data []byte)) (*nats.Subscription, error) {
	if b == nil || b.Conn == nil {
		return nil, nil
	}
	subj := "heros.fleet." + pattern
	cb := func(msg *nats.Msg) {
		handler(msg.Subject, msg.Data)
	}
	if b.JS != nil {
		return b.JS.Subscribe(subj, cb)
	}
	return b.Conn.Subscribe(subj, cb)
}
