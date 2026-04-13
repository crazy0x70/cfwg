package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"cfwg/internal/app"
	"cfwg/internal/config"
	"cfwg/internal/router"
	runtimestatus "cfwg/internal/runtime"
	"cfwg/internal/socks5"
	"cfwg/internal/state"
	"cfwg/internal/system"
	"cfwg/internal/warpapi"
	"cfwg/internal/wgdev"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type warpAPI interface {
	Register(context.Context, warpapi.RegistrationInput) (warpapi.Device, error)
	GetSourceDevice(context.Context, string, string) (warpapi.Device, error)
	UpdateLicense(context.Context, warpapi.LicenseUpdateInput) error
	SetWarpEnabled(context.Context, string, string, bool) error
}

type wireguardDeviceManager interface {
	Apply(context.Context, wgdev.DesiredConfig) error
	Delete(context.Context, string) error
}

type routeManager interface {
	Apply(context.Context, string, router.Plan) error
	Cleanup(context.Context) error
}

func buildLegacyRuntimeDependencies(getenv func(string) string, cfg runtimeConfig, status *runtimestatus.Status) (app.Dependencies, error) {
	store := state.NewStore(cfg.StateDir)
	supervisor := newSOCKS5SupervisorFunc(cfg.ProxyBinaryPath)
	runner := newSystemRunnerFunc()
	bootstrapHTTPClient := newLegacyHTTPClientFunc(15 * time.Second)
	bootstrapper := legacyBootstrapper{
		client: newWarpAPIClientFunc(cfg.WarpAPIBaseURL, bootstrapHTTPClient),
	}
	prober, err := newProberFunc(
		fmt.Sprintf("127.0.0.1:%d", defaultProxyPort),
		getenv("uname"),
		getenv("upwd"),
		cfg.LegacyProbeURL,
		cfg.LegacyProbeHost,
	)
	if err != nil {
		return app.Dependencies{}, err
	}

	wgManager := newLegacyWGManagerFunc(runner)

	return app.Dependencies{
		ConfigLoader: func() (config.Config, error) {
			return config.LoadFromEnv(getenv)
		},
		StateLoader:  store.Load,
		StateSaver:   store.Save,
		Bootstrapper: bootstrapper,
		NetworkManager: &legacyNetworkManager{
			deviceName: cfg.LegacyDeviceName,
			mtu:        cfg.LegacyMTU,
			wg:         wgManager,
			routes:     newLegacyRouteManagerFunc(runner),
		},
		Prober: prober,
		ProxyConfigWriter: func(appConfig config.Config) (string, error) {
			return writeSOCKS5Config(cfg.SOCKS5ConfigPath, appConfig, cfg.ProxyPublicHost, cfg.ProxyPublicPort, true, "")
		},
		Supervisor: supervisor,
		Status:     status,
	}, nil
}

func newHTTPProxyClient(port int, timeout time.Duration) (*http.Client, error) {
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("parse local proxy url: %w", err)
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}, nil
}

func newSOCKS5HTTPClient(serverAddr, username, password string, timeout time.Duration) (*http.Client, error) {
	var auth *socks5.AuthConfig
	if username != config.Unspecified && password != config.Unspecified && username != "" && password != "" {
		auth = &socks5.AuthConfig{
			Username: username,
			Password: password,
		}
	}

	dialer := socks5.Dialer{
		ServerAddr: serverAddr,
		Auth:       auth,
		Timeout:    timeout,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}, nil
}

type legacyBootstrapper struct {
	client warpAPI
	now    func() time.Time
}

func (b legacyBootstrapper) EnsureDevice(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
	if b.client == nil {
		return state.State{}, errors.New("warp api client is required")
	}

	current.SchemaVersion = 1

	if refreshed, err := b.refresh(ctx, current); err == nil {
		return b.applyLicense(ctx, cfg, refreshed)
	}

	if isReusableLegacyState(current) {
		return b.applyLicense(ctx, cfg, current)
	}

	created, err := b.register(ctx, cfg)
	if err != nil {
		return state.State{}, err
	}

	return b.applyLicense(ctx, cfg, created)
}

func (b legacyBootstrapper) refresh(ctx context.Context, current state.State) (state.State, error) {
	if current.DeviceID == "" || current.AccessToken == "" || current.PrivateKey == "" {
		return state.State{}, errors.New("state cannot be refreshed")
	}

	if err := b.client.SetWarpEnabled(ctx, current.DeviceID, current.AccessToken, true); err != nil {
		return state.State{}, fmt.Errorf("enable warp device: %w", err)
	}

	device, err := b.client.GetSourceDevice(ctx, current.DeviceID, current.AccessToken)
	if err != nil {
		return state.State{}, fmt.Errorf("fetch warp device: %w", err)
	}

	next, err := stateFromWarpDevice(device, current.DeviceID, current.AccessToken, current.PrivateKey, current.PublicKey)
	if err != nil {
		return state.State{}, err
	}
	if next.TunnelProtocol != "wireguard" {
		return state.State{}, fmt.Errorf("legacy backend requires wireguard tunnel protocol, got %q", next.TunnelProtocol)
	}

	return next, nil
}

func (b legacyBootstrapper) register(ctx context.Context, cfg config.Config) (state.State, error) {
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return state.State{}, fmt.Errorf("generate wireguard private key: %w", err)
	}
	publicKey := privateKey.PublicKey()

	now := time.Now
	if b.now != nil {
		now = b.now
	}

	registration, err := b.client.Register(ctx, warpapi.RegistrationInput{
		InstallID: randomInstallID(),
		Key:       publicKey.String(),
		Locale:    "en_US",
		Model:     "PC",
		TOS:       now().UTC().Format(time.RFC3339),
		Type:      "Android",
	})
	if err != nil {
		return state.State{}, fmt.Errorf("register warp device: %w", err)
	}

	deviceID := registration.ID
	token := registration.Token
	if deviceID == "" || token == "" {
		return state.State{}, errors.New("warp registration did not return device credentials")
	}

	if err := b.client.SetWarpEnabled(ctx, deviceID, token, true); err != nil {
		return state.State{}, fmt.Errorf("enable warp device: %w", err)
	}

	device, err := b.client.GetSourceDevice(ctx, deviceID, token)
	if err != nil {
		return state.State{}, fmt.Errorf("fetch warp device: %w", err)
	}

	current, err := stateFromWarpDevice(device, deviceID, token, privateKey.String(), publicKey.String())
	if err != nil {
		return state.State{}, err
	}
	if current.TunnelProtocol != "wireguard" {
		return state.State{}, fmt.Errorf("legacy backend requires wireguard tunnel protocol, got %q", current.TunnelProtocol)
	}
	if normalizeLicense(cfg.WARPLicense) != "" {
		if err := b.client.UpdateLicense(ctx, warpapi.LicenseUpdateInput{
			DeviceID: current.DeviceID,
			Token:    current.AccessToken,
			License:  normalizeLicense(cfg.WARPLicense),
		}); err != nil {
			return state.State{}, fmt.Errorf("update warp license: %w", err)
		}
		current.License = normalizeLicense(cfg.WARPLicense)
	}

	return current, nil
}

func (b legacyBootstrapper) applyLicense(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
	license := normalizeLicense(cfg.WARPLicense)
	if license == "" || license == current.License {
		return current, nil
	}
	if current.DeviceID == "" || current.AccessToken == "" {
		return state.State{}, errors.New("legacy state is missing device credentials for license update")
	}

	if err := b.client.UpdateLicense(ctx, warpapi.LicenseUpdateInput{
		DeviceID: current.DeviceID,
		Token:    current.AccessToken,
		License:  license,
	}); err != nil {
		return state.State{}, fmt.Errorf("update warp license: %w", err)
	}

	current.License = license
	return current, nil
}

func normalizeLicense(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == config.Unspecified {
		return ""
	}
	return raw
}

type legacyNetworkManager struct {
	deviceName string
	mtu        int
	wg         wireguardDeviceManager
	routes     routeManager
}

func (m *legacyNetworkManager) Apply(ctx context.Context, cfg config.Config, current state.State) error {
	if m.wg == nil {
		return errors.New("wireguard manager is required")
	}
	if m.routes == nil {
		return errors.New("route manager is required")
	}

	deviceName := valueOrDefault(m.deviceName, defaultLegacyDeviceName)
	endpointAddr := selectPeerEndpointAddress(current, cfg.ProxyStack)
	desired := wgdev.DesiredConfig{
		Name:          deviceName,
		StackMode:     cfg.ProxyStack,
		PrivateKey:    current.PrivateKey,
		AddressV4:     current.IPv4,
		AddressV6:     current.IPv6,
		PeerPublicKey: current.PeerPublicKey,
		Endpoint:      selectPeerEndpoint(current, cfg.ProxyStack),
		MTU:           m.mtu,
	}

	if err := m.wg.Apply(ctx, desired); err != nil {
		return fmt.Errorf("apply wireguard device: %w", err)
	}

	if err := m.routes.Apply(ctx, deviceName, router.BuildPlan(router.RouteInput{
		StackMode:    cfg.ProxyStack,
		PeerEndpoint: endpointAddr,
	})); err != nil {
		_ = m.routes.Cleanup(context.Background())
		_ = m.wg.Delete(context.Background(), deviceName)
		return fmt.Errorf("apply routes: %w", err)
	}

	return nil
}

func (m *legacyNetworkManager) Cleanup(ctx context.Context) error {
	var cleanupErr error
	if m.routes != nil {
		if err := m.routes.Cleanup(ctx); err != nil {
			cleanupErr = fmt.Errorf("cleanup routes: %w", err)
		}
	}
	if m.wg != nil {
		if err := m.wg.Delete(ctx, valueOrDefault(m.deviceName, defaultLegacyDeviceName)); err != nil {
			if cleanupErr != nil {
				return errors.Join(cleanupErr, fmt.Errorf("delete wireguard device: %w", err))
			}
			return fmt.Errorf("delete wireguard device: %w", err)
		}
	}

	return cleanupErr
}

type legacyConnectivityProber struct {
	probeURL string
	client   *http.Client
}

func (p legacyConnectivityProber) Check(ctx context.Context) error {
	if p.probeURL == "" {
		return errors.New("legacy probe url is required")
	}

	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.probeURL, nil)
	if err != nil {
		return fmt.Errorf("build legacy probe request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("perform legacy connectivity probe: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("legacy connectivity probe returned status %d", resp.StatusCode)
	}

	return nil
}

func stateFromWarpDevice(device warpapi.Device, fallbackDeviceID, fallbackToken, privateKey, publicKey string) (state.State, error) {
	deviceID := device.ID
	if deviceID == "" {
		deviceID = fallbackDeviceID
	}
	token := device.Token
	if token == "" {
		token = fallbackToken
	}

	if deviceID == "" || token == "" {
		return state.State{}, errors.New("warp device response is missing credentials")
	}
	if len(device.Config.Peers) == 0 {
		return state.State{}, errors.New("warp device response is missing peers")
	}

	peer := device.Config.Peers[0]
	if publicKey == "" {
		publicKey = device.Key
	}

	return state.State{
		SchemaVersion:  1,
		DeviceID:       deviceID,
		AccessToken:    token,
		PrivateKey:     privateKey,
		PublicKey:      publicKey,
		ClientID:       device.Config.ClientID,
		IPv4:           device.Config.Interface.Addresses.V4,
		IPv6:           device.Config.Interface.Addresses.V6,
		PeerPublicKey:  peer.PublicKey,
		PeerEndpoint:   peer.Endpoint.Host,
		PeerEndpointV4: joinEndpointHostAndPort(peer.Endpoint.V4, peer.Endpoint.Host),
		PeerEndpointV6: joinEndpointHostAndPort(peer.Endpoint.V6, peer.Endpoint.Host),
		License:        device.Account.License,
		WarpEnabled:    device.WarpEnabled,
		TunnelProtocol: effectiveTunnelProtocol(device),
		CreatedAt:      device.Created,
		UpdatedAt:      device.Updated,
	}, nil
}

func effectiveTunnelProtocol(device warpapi.Device) string {
	if normalized := normalizeTunnelProtocolName(device.TunnelType); normalized != "" {
		return normalized
	}

	normalizedPolicy := normalizeTunnelProtocolName(device.Policy.TunnelProtocol)
	if normalizedPolicy == "wireguard" {
		return normalizedPolicy
	}

	if deviceLooksLikeWireGuard(device) {
		return "wireguard"
	}

	return normalizedPolicy
}

func normalizeTunnelProtocolName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func deviceLooksLikeWireGuard(device warpapi.Device) bool {
	if strings.EqualFold(strings.TrimSpace(device.KeyType), "curve25519") {
		return true
	}
	if len(device.Config.Peers) == 0 {
		return false
	}

	endpoint := device.Config.Peers[0].Endpoint
	for _, port := range endpoint.Ports {
		if port == 2408 {
			return true
		}
	}

	for _, candidate := range []string{endpoint.Host, endpoint.V4, endpoint.V6} {
		if endpointPort(candidate) == "2408" {
			return true
		}
	}

	return false
}

func endpointPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if _, port, err := net.SplitHostPort(raw); err == nil {
		return port
	}

	return ""
}

func isReusableLegacyState(current state.State) bool {
	return current.SchemaVersion > 0 &&
		current.DeviceID != "" &&
		current.AccessToken != "" &&
		current.PrivateKey != "" &&
		current.PeerPublicKey != "" &&
		current.PeerEndpoint != "" &&
		current.TunnelProtocol == "wireguard"
}

func selectPeerEndpoint(current state.State, stack config.StackMode) string {
	switch stack {
	case config.Stack4:
		if endpoint := ensureEndpointPort(current.PeerEndpointV4, current.PeerEndpoint); endpoint != "" {
			return endpoint
		}
		return current.PeerEndpoint
	case config.Stack6:
		if endpoint := ensureEndpointPort(current.PeerEndpointV6, current.PeerEndpoint); endpoint != "" {
			return endpoint
		}
		return current.PeerEndpoint
	default:
		if endpoint := ensureEndpointPort(current.PeerEndpointV4, current.PeerEndpoint); endpoint != "" {
			return endpoint
		}
		if endpoint := ensureEndpointPort(current.PeerEndpointV6, current.PeerEndpoint); endpoint != "" {
			return endpoint
		}
		return current.PeerEndpoint
	}
}

func selectPeerEndpointAddress(current state.State, stack config.StackMode) netip.Addr {
	switch stack {
	case config.Stack4:
		return parseEndpointAddr(current.PeerEndpointV4)
	case config.Stack6:
		return parseEndpointAddr(current.PeerEndpointV6)
	default:
		if addr := parseEndpointAddr(current.PeerEndpointV4); addr.IsValid() {
			return addr
		}
		return parseEndpointAddr(current.PeerEndpointV6)
	}
}

func parseEndpointAddr(raw string) netip.Addr {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}
	}

	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().Unmap()
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap()
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			return addr.Unmap()
		}
	}

	return netip.Addr{}
}

func joinEndpointHostAndPort(ip, endpoint string) string {
	if ip == "" {
		return ""
	}
	ip = endpointHost(ip)
	if ip == "" {
		return ""
	}

	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return ip
	}

	return net.JoinHostPort(ip, port)
}

func endpointHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if host, _, err := net.SplitHostPort(raw); err == nil {
		return strings.Trim(host, "[]")
	}

	return strings.Trim(raw, "[]")
}

func ensureEndpointPort(raw, fallback string) string {
	if raw == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw
	}
	return joinEndpointHostAndPort(raw, fallback)
}

func randomInstallID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("install-%d", time.Now().UnixNano())
	}

	return fmt.Sprintf("%x", buf)
}

func newLegacyRouterManager(runner system.Runner) routeManager {
	return &router.Manager{Runner: runner}
}
