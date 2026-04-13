package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"cfwg/internal/app"
	"cfwg/internal/config"
	healthserver "cfwg/internal/health"
	runtimestatus "cfwg/internal/runtime"
	"cfwg/internal/socks5"
	"cfwg/internal/wgdev"
)

const (
	defaultHealthcheckURL  = "http://127.0.0.1:9090/readyz"
	defaultStateDir        = "/var/lib/warp-socks"
	defaultProxyPort       = 1080
	defaultProxyPublicPort = defaultProxyPort
	defaultProxyPublicHost = "127.0.0.1"
)

type runtimeConfig struct {
	Backend          warpBackend
	StateDir         string
	RuntimeDir       string
	SOCKS5ConfigPath string
	ProxyBinaryPath  string
	ProxyPublicHost  string
	ProxyPublicPort  int
	HealthcheckURL   string
	HealthListenAddr string
	WarpAPIBaseURL   string
	LegacyDeviceName string
	LegacyProbeURL   string
	LegacyProbeHost  string
	LegacyMTU        int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runWithEnv(ctx, os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWithEnv(ctx context.Context, args []string, getenv func(string) string) error {
	if len(args) > 0 && args[0] == "serve-socks5" {
		if len(args) != 2 {
			return errors.New("serve-socks5 requires exactly one config path argument")
		}
		return runSOCKS5Server(ctx, args[1])
	}

	cfg, err := loadRuntimeConfig(getenv)
	if err != nil {
		return err
	}

	if len(args) > 0 && args[0] == "healthcheck" {
		return healthcheck(cfg.HealthcheckURL)
	}

	if len(args) > 0 {
		return fmt.Errorf("unknown subcommand %q", args[0])
	}

	status := runtimestatus.NewStatus(time.Now().UTC())
	application, err := newRuntimeApp(getenv, cfg, status)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    cfg.HealthListenAddr,
		Handler: healthserver.NewHandler(healthStatusAdapter{status: status}),
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- fmt.Errorf("serve health endpoints: %w", err)
			cancel()
		}
	}()

	appErrCh := make(chan error, 1)
	go func() {
		appErrCh <- application.Run(runCtx)
	}()

	var runErr error
	select {
	case runErr = <-appErrCh:
	case runErr = <-serverErrCh:
		<-appErrCh
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		if runErr == nil {
			runErr = fmt.Errorf("shutdown health server: %w", err)
		}
	}

	return runErr
}

func loadRuntimeConfig(getenv func(string) string) (runtimeConfig, error) {
	if getenv == nil {
		return runtimeConfig{}, errors.New("getenv is required")
	}

	stateDir := valueOrDefault(getenv("WARP_STATE_DIR"), defaultStateDir)
	runtimeDir := valueOrDefault(getenv("WARP_RUNTIME_DIR"), filepath.Join(stateDir, "runtime"))
	healthcheckURL := valueOrDefault(getenv("HEALTHCHECK_URL"), defaultHealthcheckURL)
	healthListenAddr, err := healthListenAddrFromURL(healthcheckURL)
	if err != nil {
		return runtimeConfig{}, err
	}
	backend, err := parseWarpBackend(getenv("WARP_BACKEND"))
	if err != nil {
		return runtimeConfig{}, err
	}

	return runtimeConfig{
		Backend:          backend,
		StateDir:         stateDir,
		RuntimeDir:       runtimeDir,
		SOCKS5ConfigPath: valueOrDefault(getenv("SOCKS5_CONFIG_PATH"), filepath.Join(runtimeDir, "socks5.json")),
		ProxyBinaryPath:  valueOrDefault(getenv("PROXY_BINARY_PATH"), currentExecutablePath()),
		ProxyPublicHost:  valueOrDefault(getenv("PROXY_PUBLIC_HOST"), defaultProxyPublicHost),
		ProxyPublicPort:  parseIntOrDefault(getenv("PROXY_PUBLIC_PORT"), defaultProxyPublicPort),
		HealthcheckURL:   healthcheckURL,
		HealthListenAddr: healthListenAddr,
		WarpAPIBaseURL:   defaultWarpAPIBaseURL,
		LegacyDeviceName: defaultLegacyDeviceName,
		LegacyProbeURL:   getenv("WARP_CONNECTIVITY_PROBE_URL"),
		LegacyProbeHost:  getenv("WARP_CONNECTIVITY_PROBE_HOST"),
		LegacyMTU:        wgdev.DefaultMTU,
	}, nil
}

func newRuntimeApp(getenv func(string) string, cfg runtimeConfig, status *runtimestatus.Status) (*app.App, error) {
	deps, err := buildRuntimeDependencies(getenv, cfg, status)
	if err != nil {
		return nil, err
	}

	return app.NewApp(deps)
}

func writeSOCKS5Config(path string, cfg config.Config, publicHost string, publicPort int, allowUDP bool, upstreamSOCKS5Addr string) (string, error) {
	if publicPort <= 0 {
		publicPort = defaultProxyPublicPort
	}
	fileCfg := socks5.FileConfig{
		ListenAddr:         fmt.Sprintf(":%d", defaultProxyPort),
		PublicHost:         valueOrDefault(publicHost, defaultProxyPublicHost),
		PublicPort:         publicPort,
		AllowUDP:           allowUDP,
		UpstreamSOCKS5Addr: upstreamSOCKS5Addr,
	}
	if cfg.Auth.Enabled {
		fileCfg.Auth = &socks5.AuthConfig{
			Username: cfg.Auth.Username,
			Password: cfg.Auth.Password,
		}
	}
	if err := socks5.WriteFileConfig(path, fileCfg); err != nil {
		return "", err
	}
	return path, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func parseIntOrDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}

	return value
}

func currentExecutablePath() string {
	path, err := os.Executable()
	if err != nil || path == "" {
		return "/app/warp-socksd"
	}
	return path
}

func healthListenAddrFromURL(rawURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("parse HEALTHCHECK_URL: %w", err)
	}
	if req.URL.Host == "" {
		return "", errors.New("HEALTHCHECK_URL must include host:port")
	}

	return req.URL.Host, nil
}

func healthcheck(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned status %d", resp.StatusCode)
	}

	return nil
}

func runSOCKS5Server(ctx context.Context, configPath string) error {
	cfg, err := socks5.LoadFileConfig(configPath)
	if err != nil {
		return err
	}
	if cfg.UpstreamSOCKS5Addr != "" {
		dialer := socks5.Dialer{ServerAddr: cfg.UpstreamSOCKS5Addr}
		cfg.DialContext = dialer.DialContext
	}

	server := socks5.NewServer(cfg)
	if err := server.Start(ctx); err != nil {
		return err
	}
	defer server.Close()

	<-ctx.Done()
	return nil
}

type healthStatusAdapter struct {
	status *runtimestatus.Status
}

func (a healthStatusAdapter) Ready() bool {
	return a.status.Ready()
}

func (a healthStatusAdapter) Snapshot() interface{} {
	return a.status.Snapshot()
}
