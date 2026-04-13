package app

import (
	"context"
	"errors"
	"fmt"
	"log"

	"cfwg/internal/config"
	"cfwg/internal/state"
)

var runtimeLogf = log.Printf

type ConfigLoader func() (config.Config, error)
type StateLoader func() (state.State, error)
type StateSaver func(state.State) error
type ProxyConfigWriter func(config.Config) (string, error)

type Bootstrapper interface {
	EnsureDevice(context.Context, config.Config, state.State) (state.State, error)
}

type NetworkManager interface {
	Apply(context.Context, config.Config, state.State) error
	Cleanup(context.Context) error
}

type Supervisor interface {
	Start(context.Context, string) error
	Stop() error
	Done() <-chan error
}

type Readiness interface {
	SetReady(bool)
}

type Prober interface {
	Check(context.Context) error
}

type Dependencies struct {
	ConfigLoader      ConfigLoader
	StateLoader       StateLoader
	StateSaver        StateSaver
	Bootstrapper      Bootstrapper
	NetworkManager    NetworkManager
	ProxyConfigWriter ProxyConfigWriter
	Supervisor        Supervisor
	Prober            Prober
	Status            Readiness
}

type App struct {
	deps Dependencies
}

func NewApp(deps Dependencies) (*App, error) {
	switch {
	case deps.ConfigLoader == nil:
		return nil, errors.New("config loader is required")
	case deps.StateLoader == nil:
		return nil, errors.New("state loader is required")
	case deps.StateSaver == nil:
		return nil, errors.New("state saver is required")
	case deps.Bootstrapper == nil:
		return nil, errors.New("bootstrapper is required")
	case deps.NetworkManager == nil:
		return nil, errors.New("network manager is required")
	case deps.ProxyConfigWriter == nil:
		return nil, errors.New("proxy config writer is required")
	case deps.Supervisor == nil:
		return nil, errors.New("supervisor is required")
	case deps.Prober == nil:
		return nil, errors.New("prober is required")
	case deps.Status == nil:
		return nil, errors.New("status is required")
	default:
		return &App{deps: deps}, nil
	}
}

func (a *App) Run(ctx context.Context) error {
	cfg, err := a.deps.ConfigLoader()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	runtimeLogf("cfwg: starting runtime stack=%s auth_enabled=%t", cfg.ProxyStack, cfg.Auth.Enabled)

	loadedState, err := a.deps.StateLoader()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	runtimeLogf("cfwg: loaded state has_device=%t has_ipv4=%t has_ipv6=%t", loadedState.DeviceID != "", loadedState.IPv4 != "", loadedState.IPv6 != "")

	currentState, err := a.deps.Bootstrapper.EnsureDevice(ctx, cfg, loadedState)
	if err != nil {
		return fmt.Errorf("bootstrap device: %w", err)
	}
	runtimeLogf("cfwg: warp device ready ipv4=%s ipv6=%s endpoint=%s", printableValue(currentState.IPv4), printableValue(currentState.IPv6), printableValue(currentState.PeerEndpoint))

	if err := a.deps.StateSaver(currentState); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	runtimeLogf("cfwg: persisted runtime state")

	if err := a.runStartedRuntime(ctx, cfg, currentState); err != nil {
		if shouldRetryWithFreshState(loadedState, err) {
			runtimeLogf("cfwg: runtime failed with persisted state, retrying with fresh registration: %v", err)
			freshState, freshErr := a.deps.Bootstrapper.EnsureDevice(ctx, cfg, state.State{})
			if freshErr != nil {
				return errors.Join(err, fmt.Errorf("recover with fresh state: bootstrap device: %w", freshErr))
			}
			if freshSaveErr := a.deps.StateSaver(freshState); freshSaveErr != nil {
				return errors.Join(err, fmt.Errorf("recover with fresh state: save state: %w", freshSaveErr))
			}
			runtimeLogf("cfwg: fresh warp device ready ipv4=%s ipv6=%s endpoint=%s", printableValue(freshState.IPv4), printableValue(freshState.IPv6), printableValue(freshState.PeerEndpoint))
			if retryErr := a.runStartedRuntime(ctx, cfg, freshState); retryErr != nil {
				return errors.Join(err, fmt.Errorf("recover with fresh state: %w", retryErr))
			}
			return nil
		}
		return err
	}

	return nil
}

func (a *App) runStartedRuntime(ctx context.Context, cfg config.Config, currentState state.State) error {
	if err := a.deps.NetworkManager.Apply(ctx, cfg, currentState); err != nil {
		return fmt.Errorf("apply network: %w", err)
	}
	runtimeLogf("cfwg: network configured stack=%s endpoint=%s", cfg.ProxyStack, printableValue(currentState.PeerEndpoint))

	proxyConfigPath, err := a.deps.ProxyConfigWriter(cfg)
	if err != nil {
		return fmt.Errorf("write proxy config: %w", err)
	}
	runtimeLogf("cfwg: proxy config rendered path=%s", proxyConfigPath)

	if err := a.deps.Supervisor.Start(ctx, proxyConfigPath); err != nil {
		return fmt.Errorf("start proxy runtime: %w", err)
	}
	runtimeLogf("cfwg: proxy runtime started")

	if err := a.deps.Prober.Check(ctx); err != nil {
		_ = a.deps.Supervisor.Stop()
		_ = a.deps.NetworkManager.Cleanup(context.Background())
		return fmt.Errorf("probe warp connectivity: %w", err)
	}

	a.deps.Status.SetReady(true)
	runtimeLogf("cfwg: runtime is ready")
	var runErr error
	select {
	case <-ctx.Done():
		runtimeLogf("cfwg: shutdown requested")
	case err := <-a.deps.Supervisor.Done():
		if err != nil {
			runtimeLogf("cfwg: proxy runtime exited unexpectedly: %v", err)
			runErr = fmt.Errorf("proxy runtime exited unexpectedly: %w", err)
		}
	}
	a.deps.Status.SetReady(false)

	stopErr := a.deps.Supervisor.Stop()
	cleanupErr := a.deps.NetworkManager.Cleanup(context.Background())

	if runErr != nil {
		return runErr
	}
	if stopErr != nil {
		return fmt.Errorf("stop proxy runtime: %w", stopErr)
	}
	if cleanupErr != nil {
		return fmt.Errorf("cleanup network: %w", cleanupErr)
	}
	runtimeLogf("cfwg: runtime stopped cleanly")

	return nil
}

func shouldRetryWithFreshState(loadedState state.State, err error) bool {
	if err == nil {
		return false
	}
	return loadedState.DeviceID != "" && loadedState.AccessToken != "" && loadedState.PrivateKey != ""
}

func printableValue(value string) string {
	if value == "" {
		return "n/a"
	}
	return value
}
