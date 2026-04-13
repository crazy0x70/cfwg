package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cfwg/internal/app"
	"cfwg/internal/router"
	"cfwg/internal/socks5"
	"cfwg/internal/system"
	"cfwg/internal/warpapi"
)

func TestRunWithEnv_HealthcheckUsesConfiguredURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}))
	defer server.Close()

	err := runWithEnv(context.Background(), []string{"healthcheck"}, func(key string) string {
		if key == "HEALTHCHECK_URL" {
			return server.URL
		}

		return ""
	})
	if err != nil {
		t.Fatalf("expected healthcheck to succeed, got %v", err)
	}
}

func TestLoadRuntimeConfig_BackendSelection(t *testing.T) {
	t.Run("defaults to legacy", func(t *testing.T) {
		cfg, err := loadRuntimeConfig(func(string) string { return "" })
		if err != nil {
			t.Fatalf("load runtime config: %v", err)
		}
		if cfg.Backend != backendLegacy {
			t.Fatalf("expected default backend %q, got %q", backendLegacy, cfg.Backend)
		}
	})

	t.Run("accepts legacy", func(t *testing.T) {
		cfg, err := loadRuntimeConfig(func(key string) string {
			if key == "WARP_BACKEND" {
				return "legacy"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("load runtime config: %v", err)
		}
		if cfg.Backend != backendLegacy {
			t.Fatalf("expected backend %q, got %q", backendLegacy, cfg.Backend)
		}
	})

	t.Run("rejects invalid backend", func(t *testing.T) {
		_, err := loadRuntimeConfig(func(key string) string {
			if key == "WARP_BACKEND" {
				return "broken"
			}
			return ""
		})
		if err == nil {
			t.Fatal("expected invalid backend to fail")
		}
	})

	t.Run("parses proxy public port", func(t *testing.T) {
		cfg, err := loadRuntimeConfig(func(key string) string {
			if key == "PROXY_PUBLIC_PORT" {
				return "18080"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("load runtime config: %v", err)
		}
		if cfg.ProxyPublicPort != 18080 {
			t.Fatalf("expected proxy public port 18080, got %d", cfg.ProxyPublicPort)
		}
	})

	t.Run("accepts probe overrides", func(t *testing.T) {
		cfg, err := loadRuntimeConfig(func(key string) string {
			switch key {
			case "WARP_CONNECTIVITY_PROBE_URL":
				return "https://example.com/trace"
			case "WARP_CONNECTIVITY_PROBE_HOST":
				return "trace.example.com"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("load runtime config: %v", err)
		}
		if cfg.LegacyProbeURL != "https://example.com/trace" {
			t.Fatalf("expected probe url override, got %q", cfg.LegacyProbeURL)
		}
		if cfg.LegacyProbeHost != "trace.example.com" {
			t.Fatalf("expected probe host override, got %q", cfg.LegacyProbeHost)
		}
	})
}

func TestRunWithEnv_StartsRuntimeAndWritesArtifacts(t *testing.T) {
	stateDir := t.TempDir()
	runtimeDir := filepath.Join(stateDir, "runtime")
	socks5ConfigPath := filepath.Join(runtimeDir, "socks5.json")
	tunDevicePath := writeFakeTunDevice(t)
	healthURL := "http://" + pickFreeAddress(t) + "/readyz"
	originalWarpAPIClientFactory := newWarpAPIClientFunc
	originalLegacyWGManagerFactory := newLegacyWGManagerFunc
	originalLegacyRouteManagerFactory := newLegacyRouteManagerFunc
	originalProberFactory := newProberFunc
	originalSOCKS5SupervisorFactory := newSOCKS5SupervisorFunc
	t.Cleanup(func() {
		newWarpAPIClientFunc = originalWarpAPIClientFactory
		newLegacyWGManagerFunc = originalLegacyWGManagerFactory
		newLegacyRouteManagerFunc = originalLegacyRouteManagerFactory
		newProberFunc = originalProberFactory
		newSOCKS5SupervisorFunc = originalSOCKS5SupervisorFactory
	})

	newWarpAPIClientFunc = func(string, *http.Client) warpAPI {
		return &fakeLegacyWarpAPI{
			registerDevice: legacyStateDeviceFixture(),
			sourceDevice:   legacyStateDeviceFixture(),
		}
	}
	newLegacyWGManagerFunc = func(system.Runner) wireguardDeviceManager {
		return stubWireGuardDeviceManager{}
	}
	newLegacyRouteManagerFunc = func(system.Runner) routeManager {
		return stubRouteManager{}
	}
	newProberFunc = func(string, string, string, string, string) (app.Prober, error) {
		return noopProber{}, nil
	}
	newSOCKS5SupervisorFunc = func(string) app.Supervisor {
		return testSupervisor{done: make(chan error)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWithEnv(ctx, nil, func(key string) string {
			switch key {
			case "WARP_STATE_DIR":
				return stateDir
			case "WARP_RUNTIME_DIR":
				return runtimeDir
			case "SOCKS5_CONFIG_PATH":
				return socks5ConfigPath
			case "HEALTHCHECK_URL":
				return healthURL
			case "WARP_TUN_DEVICE_PATH":
				return tunDevicePath
			default:
				return ""
			}
		})
	}()

	waitForFile(t, socks5ConfigPath, done)

	if _, err := os.Stat(socks5ConfigPath); err != nil {
		t.Fatalf("expected socks5 config at %s, got error: %v", socks5ConfigPath, err)
	}

	statePath := filepath.Join(stateDir, "state.json")
	waitForFile(t, statePath, done)
	waitForHealthy(t, healthURL, done)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime shutdown")
	}
}

func TestRunWithEnv_ServeSOCKS5Subcommand(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "socks5.json")
	if err := socks5.WriteFileConfig(configPath, socks5.FileConfig{
		ListenAddr: "127.0.0.1:19081",
		PublicHost: "127.0.0.1",
	}); err != nil {
		t.Fatalf("write socks5 config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWithEnv(ctx, []string{"serve-socks5", configPath}, func(string) string { return "" })
	}()

	waitForTCPPort(t, "127.0.0.1:19081")

	conn, err := net.DialTimeout("tcp", "127.0.0.1:19081", 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5 child server: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write socks5 greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := conn.Read(reply); err != nil {
		t.Fatalf("read socks5 greeting: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("unexpected socks5 greeting reply: %v", reply)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean socks5 child shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for socks5 child shutdown")
	}
}

func TestRunWithEnv_ServeSOCKS5SubcommandUsesConfiguredSOCKS5Upstream(t *testing.T) {
	echoLn, echoAddr := startRuntimeTCPEchoServer(t)
	defer echoLn.Close()

	upstream := socks5.NewServer(socks5.Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		AllowUDP:   false,
	})
	if err := upstream.Start(context.Background()); err != nil {
		t.Fatalf("start upstream socks5 server: %v", err)
	}
	defer upstream.Close()

	configPath := filepath.Join(t.TempDir(), "socks5-upstream.json")
	if err := socks5.WriteFileConfig(configPath, socks5.FileConfig{
		ListenAddr:         "127.0.0.1:19082",
		PublicHost:         "127.0.0.1",
		AllowUDP:           false,
		UpstreamSOCKS5Addr: upstream.TCPAddr(),
	}); err != nil {
		t.Fatalf("write chained socks5 config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWithEnv(ctx, []string{"serve-socks5", configPath}, func(string) string { return "" })
	}()

	waitForTCPPort(t, "127.0.0.1:19082")

	dialer := socks5.Dialer{ServerAddr: "127.0.0.1:19082", Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("dial upstream echo through child socks5 server: %v", err)
	}
	defer conn.Close()

	payload := []byte("hello runtime wiring")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write runtime payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read runtime payload: %v", err)
	}
	if string(reply) != strings.ToUpper(string(payload)) {
		t.Fatalf("unexpected runtime reply: got %q want %q", string(reply), strings.ToUpper(string(payload)))
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean socks5 child shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for socks5 child shutdown")
	}
}

func writeFakeTunDevice(t *testing.T) string {
	t.Helper()

	devicePath := filepath.Join(t.TempDir(), "tun")
	if err := os.WriteFile(devicePath, []byte("fake-tun"), 0o644); err != nil {
		t.Fatalf("write fake tun device: %v", err)
	}
	return devicePath
}

func writeFakeWireGuardConfig(t *testing.T, stateDir string) {
	t.Helper()

	configPath := filepath.Join(stateDir, "cloudflare-warp", "conf.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir fake wireguard config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"tunnel_key_data":{"key_type":"curve25519","tunnel_type":"wireguard"}}`), 0o644); err != nil {
		t.Fatalf("write fake wireguard config: %v", err)
	}
}

func legacyStateDeviceFixture() warpapi.Device {
	return warpapi.Device{
		ID:          "device-id",
		Token:       "access-token",
		Key:         "public-key",
		KeyType:     "curve25519",
		TunnelType:  "wireguard",
		Created:     "2026-04-09T00:00:00Z",
		Updated:     "2026-04-09T00:00:01Z",
		WarpEnabled: true,
		Account: warpapi.Account{
			License: "",
		},
		Config: warpapi.DeviceConfig{
			ClientID: "client-id",
			Interface: warpapi.Interface{
				Addresses: warpapi.InterfaceAddresses{
					V4: "172.16.0.2",
					V6: "2606:4700:110:85c4:e805:abea:31e5:7b51",
				},
			},
			Peers: []warpapi.Peer{
				{
					PublicKey: "peer-public-key",
					Endpoint: warpapi.Endpoint{
						Host:  "engage.cloudflareclient.com:2408",
						V4:    "162.159.192.8:2408",
						V6:    "[2606:4700:d0::a29f:c008]:2408",
						Ports: []int{2408},
					},
				},
			},
		},
	}
}

func startRuntimeTCPEchoServer(t *testing.T) (net.Listener, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen runtime tcp echo: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 2048)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write([]byte(strings.ToUpper(string(buf[:n]))))
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln, ln.Addr().String()
}

func pickFreeAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on free port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().String()
}

func waitForFile(t *testing.T, path string, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}

		select {
		case err := <-done:
			t.Fatalf("runtime exited before creating %s: %v", path, err)
		default:
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("runtime never created %s", path)
}

func waitForTCPPort(t *testing.T, address string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("tcp service never became reachable at %s", address)
}

func waitForHealthy(t *testing.T, url string, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := healthcheck(url); err == nil {
			return
		}

		select {
		case err := <-done:
			t.Fatalf("runtime exited before becoming healthy at %s: %v", url, err)
		default:
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("runtime never became healthy at %s", url)
}

type testSupervisor struct {
	done chan error
}

func (s testSupervisor) Start(context.Context, string) error {
	return nil
}

func (s testSupervisor) Stop() error {
	return nil
}

func (s testSupervisor) Done() <-chan error {
	return s.done
}

type noopProber struct{}

func (noopProber) Check(context.Context) error {
	return nil
}

type stubRouteManager struct{}

func (stubRouteManager) Apply(context.Context, string, router.Plan) error { return nil }

func (stubRouteManager) Cleanup(context.Context) error { return nil }
