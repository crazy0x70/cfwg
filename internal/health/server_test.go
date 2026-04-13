package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubStatusSource struct {
	ready    bool
	snapshot interface{}
}

func (s *stubStatusSource) Ready() bool {
	return s.ready
}

func (s *stubStatusSource) Snapshot() interface{} {
	return s.snapshot
}

func TestHandler_HealthzOnlyChecksLiveness(t *testing.T) {
	handler := NewHandler(&stubStatusSource{
		ready:    false,
		snapshot: map[string]interface{}{"ready": false},
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestHandler_ReadyzReflectsRuntimeState(t *testing.T) {
	source := &stubStatusSource{
		snapshot: map[string]interface{}{"ready": false},
	}
	handler := NewHandler(source)

	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d when runtime is not ready, got %d", http.StatusServiceUnavailable, notReady.Code)
	}

	source.ready = true
	source.snapshot = map[string]interface{}{"ready": true}

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if ready.Code != http.StatusOK {
		t.Fatalf("expected status %d when runtime is ready, got %d", http.StatusOK, ready.Code)
	}
}

func TestHandler_StatusReturnsStructuredRuntimeInformation(t *testing.T) {
	source := &stubStatusSource{
		ready: true,
		snapshot: struct {
			Ready     bool   `json:"ready"`
			StartedAt string `json:"startedAt"`
		}{
			Ready:     true,
			StartedAt: "2026-04-07T08:00:00Z",
		},
	}
	handler := NewHandler(source)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected Content-Type to contain application/json, got %q", contentType)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("expected JSON response, got error: %v", err)
	}

	if ready, ok := payload["ready"].(bool); !ok || !ready {
		t.Fatalf("expected ready=true in payload, got %#v", payload["ready"])
	}
	if startedAt, ok := payload["startedAt"].(string); !ok || startedAt != "2026-04-07T08:00:00Z" {
		t.Fatalf("expected startedAt to be preserved, got %#v", payload["startedAt"])
	}
}
