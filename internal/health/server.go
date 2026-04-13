package health

import (
	"encoding/json"
	"net/http"
)

type StatusSource interface {
	Ready() bool
	Snapshot() interface{}
}

func NewHandler(source StatusSource) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		handleReadyz(w, r, source)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, source)
	})

	return mux
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleReadyz(w http.ResponseWriter, _ *http.Request, source StatusSource) {
	if source != nil && source.Ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}

	http.Error(w, "not ready", http.StatusServiceUnavailable)
}

func handleStatus(w http.ResponseWriter, _ *http.Request, source StatusSource) {
	var payload interface{} = struct{}{}
	if source != nil {
		payload = source.Snapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}
