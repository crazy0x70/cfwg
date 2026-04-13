package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"cfwg/internal/config"
	"cfwg/internal/process"
	runtimestatus "cfwg/internal/runtime"
	"cfwg/internal/socks5"
	"cfwg/internal/wgdev"
)

func TestBuildRuntimeDependencies_WiresLegacyBackend(t *testing.T) {
	configPath := t.TempDir() + "/socks5.json"
	deps, err := buildRuntimeDependencies(func(string) string { return "" }, runtimeConfig{
		Backend:          backendLegacy,
		StateDir:         t.TempDir(),
		SOCKS5ConfigPath: configPath,
		ProxyPublicHost:  "127.0.0.1",
		ProxyBinaryPath:  "/app/warp-socksd",
	}, runtimestatus.NewStatus(testNow()))
	if err != nil {
		t.Fatalf("build dependencies: %v", err)
	}

	if _, ok := deps.Bootstrapper.(legacyBootstrapper); !ok {
		t.Fatalf("expected legacy bootstrapper, got %T", deps.Bootstrapper)
	}
	if _, ok := deps.NetworkManager.(*legacyNetworkManager); !ok {
		t.Fatalf("expected legacy network manager, got %T", deps.NetworkManager)
	}
	if _, ok := deps.Supervisor.(*process.SOCKS5Supervisor); !ok {
		t.Fatalf("expected legacy supervisor to be socks5 supervisor, got %T", deps.Supervisor)
	}
	prober, ok := deps.Prober.(httpProber)
	if !ok {
		t.Fatalf("expected legacy prober, got %T", deps.Prober)
	}
	if prober.URL != defaultLegacyProbeURL {
		t.Fatalf("expected legacy probe url %q, got %q", defaultLegacyProbeURL, prober.URL)
	}
	if prober.HostHeader != defaultLegacyProbeHost {
		t.Fatalf("expected legacy probe host header %q, got %q", defaultLegacyProbeHost, prober.HostHeader)
	}
	if prober.Client == nil {
		t.Fatal("expected legacy prober to include a proxied http client")
	}
	transport, ok := prober.Client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatalf("expected legacy prober transport to use a socks5 dialer, got %#v", prober.Client.Transport)
	}

	renderedPath, err := deps.ProxyConfigWriter(config.Config{})
	if err != nil {
		t.Fatalf("write socks5 config: %v", err)
	}
	rendered, err := os.ReadFile(renderedPath)
	if err != nil {
		t.Fatalf("read socks5 config: %v", err)
	}
	var socksCfg socks5.FileConfig
	if err := json.Unmarshal(rendered, &socksCfg); err != nil {
		t.Fatalf("decode socks5 runtime config: %v\n%s", err, rendered)
	}
	if socksCfg.ListenAddr != ":1080" {
		t.Fatalf("expected listen addr :1080, got %q", socksCfg.ListenAddr)
	}
	if socksCfg.PublicHost != "127.0.0.1" {
		t.Fatalf("expected public host 127.0.0.1, got %q", socksCfg.PublicHost)
	}
	if socksCfg.PublicPort != defaultProxyPort {
		t.Fatalf("expected public port %d, got %d", defaultProxyPort, socksCfg.PublicPort)
	}
	if socksCfg.Auth != nil {
		t.Fatalf("expected anonymous socks5 config by default, got %#v", socksCfg.Auth)
	}
	if !socksCfg.AllowUDP {
		t.Fatalf("expected legacy socks5 config to enable udp, got %#v", socksCfg)
	}
	if socksCfg.UpstreamSOCKS5Addr != "" {
		t.Fatalf("expected legacy socks5 config to dial directly, got upstream %q", socksCfg.UpstreamSOCKS5Addr)
	}
}

func TestBuildRuntimeDependencies_WiresLegacyProbeOverrides(t *testing.T) {
	deps, err := buildRuntimeDependencies(func(string) string { return "" }, runtimeConfig{
		Backend:          backendLegacy,
		StateDir:         t.TempDir(),
		SOCKS5ConfigPath: t.TempDir() + "/socks5.json",
		ProxyPublicHost:  "127.0.0.1",
		ProxyBinaryPath:  "/app/warp-socksd",
		LegacyProbeURL:   "https://example.com/trace",
		LegacyProbeHost:  "trace.example.com",
	}, runtimestatus.NewStatus(testNow()))
	if err != nil {
		t.Fatalf("build dependencies: %v", err)
	}

	prober, ok := deps.Prober.(httpProber)
	if !ok {
		t.Fatalf("expected legacy prober, got %T", deps.Prober)
	}
	if prober.URL != "https://example.com/trace" {
		t.Fatalf("expected overridden probe url, got %q", prober.URL)
	}
	if prober.HostHeader != "trace.example.com" {
		t.Fatalf("expected overridden probe host, got %q", prober.HostHeader)
	}
}

type stubWireGuardDeviceManager struct{}

func (stubWireGuardDeviceManager) Apply(context.Context, wgdev.DesiredConfig) error { return nil }

func (stubWireGuardDeviceManager) Delete(context.Context, string) error { return nil }

func testNow() time.Time {
	return time.Unix(0, 0).UTC()
}
