package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProber_CheckUsesHostHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "cloudflare.com" {
			t.Fatalf("expected host header cloudflare.com, got %q", r.Host)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("warp=on"))
	}))
	defer server.Close()

	prober := httpProber{
		URL:        server.URL,
		HostHeader: "cloudflare.com",
		Client:     server.Client(),
	}

	if err := prober.Check(context.Background()); err != nil {
		t.Fatalf("expected prober with host header to pass, got %v", err)
	}
}
