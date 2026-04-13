package main

import (
	"context"
	"testing"

	"cfwg/internal/config"
	"cfwg/internal/state"
	"cfwg/internal/warpapi"
)

func TestLegacyBootstrapper_RefreshPreservesExistingAccessTokenWhenSourceDeviceOmitsIt(t *testing.T) {
	bootstrapper := legacyBootstrapper{
		client: &fakeLegacyWarpAPI{
			sourceDevice: warpapi.Device{
				ID:          "dev-1",
				Token:       "",
				WarpEnabled: true,
				Policy:      warpapi.Policy{TunnelProtocol: "wireguard"},
				Account:     warpapi.Account{License: "LIC-1"},
				Config: warpapi.DeviceConfig{
					ClientID: "client-1",
					Interface: warpapi.Interface{
						Addresses: warpapi.InterfaceAddresses{
							V4: "172.16.0.2",
							V6: "2606:4700:110:8765::2",
						},
					},
					Peers: []warpapi.Peer{
						{
							PublicKey: "peer-pub",
							Endpoint: warpapi.Endpoint{
								Host: "engage.cloudflareclient.com:2408",
								V4:   "162.159.193.10",
								V6:   "2606:4700:d0::a29f:c00a",
							},
						},
					},
				},
			},
		},
	}

	got, err := bootstrapper.refresh(context.Background(), state.State{
		SchemaVersion: 1,
		DeviceID:      "dev-1",
		AccessToken:   "access-1",
		PrivateKey:    "priv-1",
		PublicKey:     "pub-1",
	})
	if err != nil {
		t.Fatalf("ensure device: %v", err)
	}

	if got.AccessToken != "access-1" {
		t.Fatalf("expected existing access token to be preserved, got %q", got.AccessToken)
	}
}

func TestLegacyBootstrapper_RegisterPreservesRegistrationTokenWhenSourceDeviceOmitsIt(t *testing.T) {
	bootstrapper := legacyBootstrapper{
		client: &fakeLegacyWarpAPI{
			registerDevice: warpapi.Device{
				ID:    "dev-1",
				Token: "access-1",
			},
			sourceDevice: warpapi.Device{
				ID:          "dev-1",
				Token:       "",
				Key:         "pub-1",
				WarpEnabled: true,
				Policy:      warpapi.Policy{TunnelProtocol: "wireguard"},
				Account:     warpapi.Account{License: "LIC-1"},
				Config: warpapi.DeviceConfig{
					ClientID: "client-1",
					Interface: warpapi.Interface{
						Addresses: warpapi.InterfaceAddresses{
							V4: "172.16.0.2",
							V6: "2606:4700:110:8765::2",
						},
					},
					Peers: []warpapi.Peer{
						{
							PublicKey: "peer-pub",
							Endpoint: warpapi.Endpoint{
								Host: "engage.cloudflareclient.com:2408",
								V4:   "162.159.193.10",
								V6:   "2606:4700:d0::a29f:c00a",
							},
						},
					},
				},
			},
		},
	}

	got, err := bootstrapper.EnsureDevice(context.Background(), config.Config{}, state.State{})
	if err != nil {
		t.Fatalf("ensure device: %v", err)
	}

	if got.AccessToken != "access-1" {
		t.Fatalf("expected registration token to be preserved, got %q", got.AccessToken)
	}
}

func TestStateFromWarpDevice_DerivesWireGuardWhenPolicyStillReportsMasque(t *testing.T) {
	got, err := stateFromWarpDevice(warpapi.Device{
		ID:          "dev-1",
		Token:       "access-1",
		Key:         "pub-1",
		WarpEnabled: true,
		Policy:      warpapi.Policy{TunnelProtocol: "masque"},
		Account:     warpapi.Account{License: "LIC-1"},
		Config: warpapi.DeviceConfig{
			ClientID: "client-1",
			Interface: warpapi.Interface{
				Addresses: warpapi.InterfaceAddresses{
					V4: "172.16.0.2",
					V6: "2606:4700:110:8765::2",
				},
			},
			Peers: []warpapi.Peer{
				{
					PublicKey: "peer-pub",
					Endpoint: warpapi.Endpoint{
						Host:  "engage.cloudflareclient.com:2408",
						V4:    "162.159.193.10:0",
						V6:    "[2606:4700:d0::a29f:c00a]:0",
						Ports: []int{2408, 500, 1701, 4500},
					},
				},
			},
		},
	}, "", "", "priv-1", "")
	if err != nil {
		t.Fatalf("state from warp device: %v", err)
	}

	if got.TunnelProtocol != "wireguard" {
		t.Fatalf("expected effective tunnel protocol %q, got %q", "wireguard", got.TunnelProtocol)
	}
	if got.PeerEndpointV4 != "162.159.193.10:2408" {
		t.Fatalf("expected normalized ipv4 peer endpoint %q, got %q", "162.159.193.10:2408", got.PeerEndpointV4)
	}
	if got.PeerEndpointV6 != "[2606:4700:d0::a29f:c00a]:2408" {
		t.Fatalf("expected normalized ipv6 peer endpoint %q, got %q", "[2606:4700:d0::a29f:c00a]:2408", got.PeerEndpointV6)
	}
}

func TestLegacyBootstrapper_RefreshRejectsMasqueWhenDeviceDoesNotLookLikeWireGuard(t *testing.T) {
	bootstrapper := legacyBootstrapper{
		client: &fakeLegacyWarpAPI{
			sourceDevice: warpapi.Device{
				ID:          "dev-1",
				Token:       "access-1",
				WarpEnabled: true,
				Policy:      warpapi.Policy{TunnelProtocol: "masque"},
				Account:     warpapi.Account{License: "LIC-1"},
				Config: warpapi.DeviceConfig{
					ClientID: "client-1",
					Interface: warpapi.Interface{
						Addresses: warpapi.InterfaceAddresses{
							V4: "172.16.0.2",
							V6: "2606:4700:110:8765::2",
						},
					},
					Peers: []warpapi.Peer{
						{
							PublicKey: "peer-pub",
							Endpoint: warpapi.Endpoint{
								Host: "engage.cloudflareclient.com:443",
								V4:   "162.159.193.10:443",
								V6:   "[2606:4700:d0::a29f:c00a]:443",
							},
						},
					},
				},
			},
		},
	}

	_, err := bootstrapper.refresh(context.Background(), state.State{
		SchemaVersion: 1,
		DeviceID:      "dev-1",
		AccessToken:   "access-1",
		PrivateKey:    "priv-1",
		PublicKey:     "pub-1",
	})
	if err == nil {
		t.Fatal("expected refresh to reject non-wireguard-looking masque device")
	}
	if got := err.Error(); got != `legacy backend requires wireguard tunnel protocol, got "masque"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelectPeerEndpoint_PrefersResolvedEndpointAddresses(t *testing.T) {
	current := state.State{
		PeerEndpoint:   "engage.cloudflareclient.com:2408",
		PeerEndpointV4: "162.159.193.10:2408",
		PeerEndpointV6: "[2606:4700:d0::a29f:c00a]:2408",
	}

	if got := selectPeerEndpoint(current, config.Stack4); got != "162.159.193.10:2408" {
		t.Fatalf("expected ipv4 stack to prefer resolved endpoint, got %q", got)
	}
	if got := selectPeerEndpoint(current, config.Stack6); got != "[2606:4700:d0::a29f:c00a]:2408" {
		t.Fatalf("expected ipv6 stack to prefer resolved endpoint, got %q", got)
	}
	if got := selectPeerEndpoint(current, config.StackDual); got != "162.159.193.10:2408" {
		t.Fatalf("expected auto stack to prefer resolved ipv4 endpoint first, got %q", got)
	}
}

type fakeLegacyWarpAPI struct {
	registerDevice warpapi.Device
	sourceDevice   warpapi.Device
}

func (f *fakeLegacyWarpAPI) Register(context.Context, warpapi.RegistrationInput) (warpapi.Device, error) {
	return f.registerDevice, nil
}

func (f *fakeLegacyWarpAPI) GetSourceDevice(context.Context, string, string) (warpapi.Device, error) {
	return f.sourceDevice, nil
}

func (f *fakeLegacyWarpAPI) UpdateLicense(context.Context, warpapi.LicenseUpdateInput) error {
	return nil
}

func (f *fakeLegacyWarpAPI) SetWarpEnabled(context.Context, string, string, bool) error {
	return nil
}
