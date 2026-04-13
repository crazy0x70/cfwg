package state

import (
	"path/filepath"
	"testing"
)

func TestStore_LoadReturnsEmptyStateWhenFileIsMissing(t *testing.T) {
	store := NewStore(t.TempDir())

	got, err := store.Load()
	if err != nil {
		t.Fatalf("expected missing state file to be allowed, got %v", err)
	}

	if got != (State{}) {
		t.Fatalf("expected zero state when file missing, got %#v", got)
	}
}

func TestStore_LoadDecodesSavedState(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	want := State{
		SchemaVersion:  1,
		DeviceID:       "device-1",
		AccessToken:    "access-1",
		PrivateKey:     "priv-1",
		PublicKey:      "pub-1",
		ClientID:       "client-1",
		IPv4:           "172.16.0.2",
		IPv6:           "2606:4700:110:8765::2",
		PeerPublicKey:  "peer-pub",
		PeerEndpoint:   "engage.cloudflareclient.com:2408",
		PeerEndpointV4: "162.159.193.10",
		PeerEndpointV6: "2606:4700:d0::a29f:c00a",
		License:        "LIC-1",
		WarpEnabled:    true,
		TunnelProtocol: "wireguard",
		CreatedAt:      "2026-04-07T08:00:00Z",
		UpdatedAt:      "2026-04-07T08:01:00Z",
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("save state: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("expected state load to succeed, got %v", err)
	}

	if got != want {
		t.Fatalf("expected loaded state %#v, got %#v (path %s)", want, got, filepath.Join(dir, stateFileName))
	}
}
