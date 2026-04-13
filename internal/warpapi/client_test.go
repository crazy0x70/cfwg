package warpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_RegisterBuildsExpectedRequest(t *testing.T) {
	input := RegistrationInput{
		FCMToken:  "fcm-token",
		InstallID: "install-1",
		Key:       "public-key",
		Locale:    "en_US",
		Model:     "PC",
		TOS:       "2026-04-07T08:00:00Z",
		Type:      "Android",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected %s, got %s", http.MethodPost, r.Method)
		}
		if r.URL.Path != "/v0a1922/reg" {
			t.Fatalf("expected path %q, got %q", "/v0a1922/reg", r.URL.Path)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("expected JSON content type, got %q", contentType)
		}

		var got RegistrationInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got != input {
			t.Fatalf("expected payload %#v, got %#v", input, got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1","token":"access-1","account":{"license":"LIC-1"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	device, err := client.Register(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	if device.ID != "dev-1" {
		t.Fatalf("expected device id %q, got %q", "dev-1", device.ID)
	}
	if device.Token != "access-1" {
		t.Fatalf("expected access token %q, got %q", "access-1", device.Token)
	}
	if device.Account.License != "LIC-1" {
		t.Fatalf("expected license %q, got %q", "LIC-1", device.Account.License)
	}
}

func TestClient_UpdateLicenseBuildsExpectedRequest(t *testing.T) {
	input := LicenseUpdateInput{
		DeviceID: "dev-1",
		Token:    "access-1",
		License:  "LIC-NEW",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected %s, got %s", http.MethodPut, r.Method)
		}
		if r.URL.Path != "/v0a1922/reg/dev-1/account" {
			t.Fatalf("expected path %q, got %q", "/v0a1922/reg/dev-1/account", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer access-1" {
			t.Fatalf("expected bearer token, got %q", authorization)
		}

		var got struct {
			License string `json:"license"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got.License != input.License {
			t.Fatalf("expected license %q, got %q", input.License, got.License)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	if err := client.UpdateLicense(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}

func TestClient_GetSourceDeviceBuildsExpectedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected %s, got %s", http.MethodGet, r.Method)
		}
		if r.URL.Path != "/v0a1922/reg/dev-1" {
			t.Fatalf("expected path %q, got %q", "/v0a1922/reg/dev-1", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer access-1" {
			t.Fatalf("expected bearer token, got %q", authorization)
		}
		if userAgent := r.Header.Get("User-Agent"); userAgent == "" {
			t.Fatal("expected client to send user agent header")
		}
		if clientVersion := r.Header.Get("CF-Client-Version"); clientVersion == "" {
			t.Fatal("expected client to send CF-Client-Version header")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"dev-1",
			"token":"ignored",
			"key":"pubkey",
			"key_type":"curve25519",
			"tunnel_type":"wireguard",
			"created":"2026-04-07T08:00:00Z",
			"updated":"2026-04-07T08:01:00Z",
			"warp_enabled": true,
			"policy": {"tunnel_protocol":"wireguard"},
			"account":{"license":"LIC-1"},
			"config":{
				"client_id":"client-1",
				"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:8765::2"}},
				"peers":[{"public_key":"peer-pub","endpoint":{"host":"engage.cloudflareclient.com:2408","v4":"162.159.193.10","v6":"2606:4700:d0::a29f:c00a","ports":[2408,500,1701,4500]}}]
			}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	device, err := client.GetSourceDevice(context.Background(), "dev-1", "access-1")
	if err != nil {
		t.Fatal(err)
	}

	if device.Config.ClientID != "client-1" {
		t.Fatalf("expected client id %q, got %q", "client-1", device.Config.ClientID)
	}
	if got := device.Config.Interface.Addresses.V4; got != "172.16.0.2" {
		t.Fatalf("expected ipv4 address %q, got %q", "172.16.0.2", got)
	}
	if len(device.Config.Peers) != 1 {
		t.Fatalf("expected one peer, got %d", len(device.Config.Peers))
	}
	if got := device.Config.Peers[0].Endpoint.Host; got != "engage.cloudflareclient.com:2408" {
		t.Fatalf("expected endpoint host %q, got %q", "engage.cloudflareclient.com:2408", got)
	}
	if got := device.KeyType; got != "curve25519" {
		t.Fatalf("expected key type %q, got %q", "curve25519", got)
	}
	if got := device.TunnelType; got != "wireguard" {
		t.Fatalf("expected tunnel type %q, got %q", "wireguard", got)
	}
	if got := device.Config.Peers[0].Endpoint.Ports; len(got) != 4 || got[0] != 2408 {
		t.Fatalf("expected endpoint ports to include 2408, got %#v", got)
	}
	if !device.WarpEnabled {
		t.Fatal("expected warp_enabled to be decoded")
	}
	if device.Policy.TunnelProtocol != "wireguard" {
		t.Fatalf("expected tunnel protocol %q, got %q", "wireguard", device.Policy.TunnelProtocol)
	}
}

func TestClient_SetWarpEnabledBuildsExpectedPatchRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected %s, got %s", http.MethodPatch, r.Method)
		}
		if r.URL.Path != "/v0a1922/reg/dev-1" {
			t.Fatalf("expected path %q, got %q", "/v0a1922/reg/dev-1", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer access-1" {
			t.Fatalf("expected bearer token, got %q", authorization)
		}

		var body struct {
			WarpEnabled bool `json:"warp_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode patch body: %v", err)
		}
		if !body.WarpEnabled {
			t.Fatalf("expected warp_enabled=true payload, got %#v", body)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	if err := client.SetWarpEnabled(context.Background(), "dev-1", "access-1", true); err != nil {
		t.Fatal(err)
	}
}
