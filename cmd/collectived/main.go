// collectived: HTTP ingest for fleet proposals; optionally fans out to NATS (same subject as agentd).
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type proposal struct {
	ID        string `json:"id"`
	Layer     string `json:"layer"`
	Title     string `json:"title"`
	Rationale string `json:"rationale"`
	DiffText  string `json:"diff_text"`
}

var mu sync.Mutex
var inbox []proposal

func main() {
	addr := ":8790"
	if v := os.Getenv("COLLECTIVE_LISTEN"); v != "" {
		addr = v
	}
	var nc *nats.Conn
	if u := os.Getenv("NATS_URL"); u != "" {
		var err error
		nc, err = nats.Connect(u, nats.Name("collectived"), nats.Timeout(10*time.Second))
		if err != nil {
			log.Fatalf("nats: %v", err)
		}
		defer nc.Close()
		log.Printf("connected NATS %s", u)
	}

	http.HandleFunc("POST /v1/ingest/proposal", func(w http.ResponseWriter, r *http.Request) {
		var p proposal
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		mu.Lock()
		inbox = append(inbox, p)
		mu.Unlock()
		log.Printf("ingest proposal %s layer=%s title=%s", p.ID, p.Layer, p.Title)
		if nc != nil {
			b, _ := json.Marshal(p)
			if err := nc.Publish("heros.fleet.proposals.pending", b); err != nil {
				log.Printf("nats publish: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	})
	http.HandleFunc("GET /v1/skills/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[],"edges":[],"note":"fleet graph placeholder"}`))
	})
	log.Printf("collectived listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
