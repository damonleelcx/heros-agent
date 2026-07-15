package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Embedder turns text into dense vectors for Qdrant / hybrid retrieval.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Dims() int
}

// Naive deterministic embedder (no external calls). Good for air-gapped dev.
type Naive struct {
	Dim int
}

func (n Naive) Dims() int {
	if n.Dim <= 0 {
		return 128
	}
	return n.Dim
}

func (n Naive) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		out[i] = naiveVec(text, n.Dims())
	}
	return out, nil
}

func naiveVec(text string, dim int) []float64 {
	v := make([]float64, dim)
	t := strings.ToLower(text)
	for i, r := range t {
		if i >= 512 {
			break
		}
		v[int(r)%dim] += 1
	}
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return v
	}
	inv := 1 / math.Sqrt(sum)
	for i := range v {
		v[i] *= inv
	}
	return v
}

// OpenAI uses /v1/embeddings (configurable base URL).
type OpenAI struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int // e.g. 256..1536 for text-embedding-3-small
	HTTP       *http.Client
}

func (o OpenAI) Dims() int {
	if o.Dimensions <= 0 {
		return 1536
	}
	return o.Dimensions
}

func (o OpenAI) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o OpenAI) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if o.APIKey == "" {
		return nil, fmt.Errorf("openai embedder: missing api key")
	}
	base := strings.TrimRight(o.BaseURL, "/")
	model := o.Model
	if model == "" {
		model = "text-embedding-3-small"
	}
	body := map[string]any{
		"model": model,
		"input": texts,
	}
	if d := o.Dims(); d > 0 && d < 1536 {
		body["dimensions"] = d
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings %s: %s", resp.Status, string(rb))
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: expected %d vectors, got %d", len(texts), len(parsed.Data))
	}
	out := make([][]float64, len(parsed.Data))
	for i := range parsed.Data {
		out[i] = parsed.Data[i].Embedding
		if want := o.Dims(); len(out[i]) != want && len(out[i]) > 0 {
			if len(out[i]) > want {
				out[i] = out[i][:want]
			}
		}
	}
	return out, nil
}

// NewFromConfig picks OpenAI when key+model set, else naive.
func NewFromConfig(baseURL, apiKey, model string, dims int) Embedder {
	if apiKey != "" && model != "" {
		return OpenAI{BaseURL: baseURL, APIKey: apiKey, Model: model, Dimensions: dims}
	}
	return Naive{Dim: dims}
}
