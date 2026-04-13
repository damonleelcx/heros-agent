package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal Qdrant REST client (enterprise vector tier).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("api-key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return rb, resp.StatusCode, nil
}

// EnsureCollection creates collection with cosine distance if missing.
func (c *Client) EnsureCollection(ctx context.Context, name string, vectorSize uint64) error {
	rb, code, err := c.do(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return err
	}
	if code == 200 {
		return nil
	}
	payload := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": "Cosine",
		},
	}
	rb2, code2, err := c.do(ctx, http.MethodPut, "/collections/"+name, payload)
	if err != nil {
		return err
	}
	if code2 < 200 || code2 >= 300 {
		return fmt.Errorf("qdrant create collection %s: %s %s", name, http.StatusText(code2), string(rb2))
	}
	_ = rb
	return nil
}

type SearchHit struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
	Text    string
}

// UpsertPoints writes vectors + payload (id must be UUID or unsigned int per Qdrant rules — we use string UUIDs).
// DeletePointsByFilter removes points matching a Qdrant filter (e.g. vault_file_key + tenant_id).
func (c *Client) DeletePointsByFilter(ctx context.Context, collection string, filter map[string]any) error {
	body := map[string]any{"filter": filter}
	rb, code, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("qdrant delete filter: %s %s", http.StatusText(code), string(rb))
	}
	return nil
}

func (c *Client) UpsertPoints(ctx context.Context, collection string, points []Point) error {
	body := map[string]any{"points": points}
	rb, code, err := c.do(ctx, http.MethodPut, "/collections/"+collection+"/points?wait=true", body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("qdrant upsert: %s %s", http.StatusText(code), string(rb))
	}
	return nil
}

type Point struct {
	ID      any              `json:"id"`
	Vector  []float64        `json:"vector"`
	Payload map[string]any   `json:"payload,omitempty"`
}

// Search performs k-NN in collection.
func (c *Client) Search(ctx context.Context, collection string, vector []float64, limit int, withPayload bool) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 8
	}
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": withPayload,
	}
	rb, code, err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/search", body)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("qdrant search: %s %s", http.StatusText(code), string(rb))
	}
	var parsed struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, err
	}
	var out []SearchHit
	for _, r := range parsed.Result {
		h := SearchHit{Score: r.Score, Payload: r.Payload}
		switch v := r.ID.(type) {
		case string:
			h.ID = v
		case float64:
			h.ID = fmt.Sprintf("%.0f", v)
		default:
			h.ID = fmt.Sprint(v)
		}
		if r.Payload != nil {
			if t, ok := r.Payload["text"].(string); ok {
				h.Text = t
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// Health calls /readyz or /collections.
func (c *Client) Health(ctx context.Context) error {
	rb, code, err := c.do(ctx, http.MethodGet, "/readyz", nil)
	if err != nil {
		return err
	}
	if code == 404 {
		_, code2, err2 := c.do(ctx, http.MethodGet, "/collections", nil)
		if err2 != nil {
			return err2
		}
		if code2 < 200 || code2 >= 300 {
			return fmt.Errorf("qdrant health: %s", http.StatusText(code2))
		}
		return nil
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("qdrant readyz: %s %s", http.StatusText(code), string(rb))
	}
	return nil
}
