package main

import (
	"fmt"
	"net/http"
	"time"

	"cfwg/internal/app"
	"cfwg/internal/process"
	runtimestatus "cfwg/internal/runtime"
	"cfwg/internal/system"
	"cfwg/internal/warpapi"
	"cfwg/internal/wgdev"
)

type warpBackend string

const backendLegacy warpBackend = "legacy"

const (
	defaultWarpAPIBaseURL   = "https://api.cloudflareclient.com"
	defaultLegacyDeviceName = "wgcf"
	defaultLegacyProbeURLV4 = "http://1.1.1.1/cdn-cgi/trace"
	defaultLegacyProbeURLV6 = "http://[2606:4700:4700::1111]/cdn-cgi/trace"
	defaultLegacyProbeHost  = "cloudflare.com"
)

var newSystemRunnerFunc = func() system.Runner { return system.ExecRunner{} }
var newProberFunc = func(serverAddr, username, password, probeURL, probeHostHeader string) (app.Prober, error) {
	client, err := newSOCKS5HTTPClient(serverAddr, username, password, 10*time.Second)
	if err != nil {
		return nil, err
	}

	if probeURL != "" {
		return httpProber{
			URL:        probeURL,
			HostHeader: probeHostHeader,
			Client:     client,
		}, nil
	}

	return httpProber{
		URL:        defaultLegacyProbeURLV4,
		HostHeader: defaultLegacyProbeHost,
		FallbackTargets: []httpProbeTarget{
			{
				URL:        defaultLegacyProbeURLV6,
				HostHeader: defaultLegacyProbeHost,
			},
		},
		Client: client,
	}, nil
}
var newWarpAPIClientFunc = func(baseURL string, httpClient *http.Client) warpAPI {
	return warpapi.NewClient(baseURL, httpClient)
}
var newLegacyWGManagerFunc = func(runner system.Runner) wireguardDeviceManager {
	return wgdev.Manager{Runner: runner}
}
var newSOCKS5SupervisorFunc = func(binaryPath string) app.Supervisor {
	return &process.SOCKS5Supervisor{BinaryPath: binaryPath}
}
var newLegacyRouteManagerFunc = func(runner system.Runner) routeManager {
	return newLegacyRouterManager(runner)
}
var newLegacyHTTPClientFunc = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func parseWarpBackend(raw string) (warpBackend, error) {
	switch valueOrDefault(raw, string(backendLegacy)) {
	case string(backendLegacy):
		return backendLegacy, nil
	default:
		return "", fmt.Errorf("WARP_BACKEND must be legacy")
	}
}

func buildRuntimeDependencies(getenv func(string) string, cfg runtimeConfig, status *runtimestatus.Status) (app.Dependencies, error) {
	switch cfg.Backend {
	case "", backendLegacy:
		return buildLegacyRuntimeDependencies(getenv, cfg, status)
	default:
		return app.Dependencies{}, fmt.Errorf("unsupported warp backend %q", cfg.Backend)
	}
}
