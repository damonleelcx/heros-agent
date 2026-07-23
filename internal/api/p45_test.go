package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/scorecard"
)

type stubScorecard struct {
	view scorecard.View
	ok   bool
}

func (s *stubScorecard) Scorecard(variantID string) (scorecard.View, bool) {
	v := s.view
	v.VariantID = variantID
	return v, s.ok
}

// Same three distinct answers as P4 (route absent → 404, mounted+nil → 503, unknown variant → 404).
func TestP45MountedWithNoSourceIs503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP45(nil)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p45/variants/v/scorecard", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when mounted without a source, got %d", rec.Code)
	}
}

func TestP45NotMountedIsRouteAbsent(t *testing.T) {
	s := New(nil, config.Config{})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p45/variants/v/scorecard", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unregistered route should 404, got %d", rec.Code)
	}
}

func TestP45UnknownVariantIs404(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP45(&stubScorecard{ok: false})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p45/variants/nope/scorecard", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown variant should 404, got %d", rec.Code)
	}
}

func TestP45ServesScorecardJSON(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP45(&stubScorecard{ok: true, view: scorecard.View{State: scorecard.StateReady, ReadOnly: true}})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p45/variants/v-sc/scorecard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got scorecard.View
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.VariantID != "v-sc" || !got.ReadOnly {
		t.Fatalf("unexpected view: %+v", got)
	}
}

// The served UI page carries no apply/change control (task 10.5) — the read-only guarantee is visible
// in the HTML, not only in the JSON.
func TestP45UIHasNoApplyAffordance(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountP45(&stubScorecard{ok: true})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p45/scorecard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for UI, got %d", rec.Code)
	}
	body := strings.ToLower(rec.Body.String())
	// No apply/submit/mutate affordances in the read-only scorecard.
	for _, forbidden := range []string{"apply change", "submit proposal", `type="submit"`, "<form"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("scorecard UI contains a mutation affordance: %q", forbidden)
		}
	}
	// It DOES state its read-only nature.
	if !strings.Contains(body, "read-only") {
		t.Error("scorecard UI should declare it is read-only")
	}
}
