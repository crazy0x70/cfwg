package app

import (
	"context"
	"errors"
	"fmt"

	"cfwg/internal/config"
	"cfwg/internal/state"
)

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

	loadedState, err := a.deps.StateLoader()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	currentState, err := a.deps.Bootstrapper.EnsureDevice(ctx, cfg, loadedState)
	if err != nil {
		return fmt.Errorf("bootstrap device: %w", err)
	}

	if err := a.deps.StateSaver(currentState); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	if err := a.runStartedRuntime(ctx, cfg, currentState); err != nil {
		if shouldRetryWithFreshState(loadedState, err) {
			freshState, freshErr := a.deps.Bootstrapper.EnsureDevice(ctx, cfg, state.State{})
			if freshErr != nil {
				return errors.Join(err, fmt.Errorf("recover with fresh state: bootstrap device: %w", freshErr))
			}
			if freshSaveErr := a.deps.StateSaver(freshState); freshSaveErr != nil {
				return errors.Join(err, fmt.Errorf("recover with fresh state: save state: %w", freshSaveErr))
			}
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

	proxyConfigPath, err := a.deps.ProxyConfigWriter(cfg)
	if err != nil {
		return fmt.Errorf("write proxy config: %w", err)
	}

	if err := a.deps.Supervisor.Start(ctx, proxyConfigPath); err != nil {
		return fmt.Errorf("start proxy runtime: %w", err)
	}

	if err := a.deps.Prober.Check(ctx); err != nil {
		_ = a.deps.Supervisor.Stop()
		_ = a.deps.NetworkManager.Cleanup(context.Background())
		return fmt.Errorf("probe warp connectivity: %w", err)
	}

	a.deps.Status.SetReady(true)
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-a.deps.Supervisor.Done():
		if err != nil {
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

	return nil
}

func shouldRetryWithFreshState(loadedState state.State, err error) bool {
	if err == nil {
		return false
	}
	return loadedState.DeviceID != "" && loadedState.AccessToken != "" && loadedState.PrivateKey != ""
}
