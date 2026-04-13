package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"cfwg/internal/config"
	"cfwg/internal/state"
)

func TestNewApp_RejectsNilDependencies(t *testing.T) {
	_, err := NewApp(Dependencies{})
	if err == nil {
		t.Fatal("expected dependency validation error")
	}
}

func TestApp_RunStartsDependenciesInOrder(t *testing.T) {
	var (
		mu          sync.Mutex
		order       []string
		readyStates []bool
		started     = make(chan struct{}, 1)
	)

	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}

	status := &statusStub{
		setReady: func(ready bool) {
			mu.Lock()
			defer mu.Unlock()
			readyStates = append(readyStates, ready)
		},
	}

	app, err := NewApp(Dependencies{
		ConfigLoader: func() (config.Config, error) {
			record("config")
			return config.Config{ProxyStack: config.StackDual}, nil
		},
		StateLoader: func() (state.State, error) {
			record("load-state")
			return state.State{SchemaVersion: 1}, nil
		},
		StateSaver: func(s state.State) error {
			record("save-state")
			return nil
		},
		Bootstrapper: bootstrapperStub{
			ensureDevice: func(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
				record("bootstrap")
				current.DeviceID = "device-1"
				return current, nil
			},
		},
		NetworkManager: networkManagerStub{
			apply: func(ctx context.Context, cfg config.Config, current state.State) error {
				record("network-apply")
				return nil
			},
			cleanup: func(ctx context.Context) error {
				record("network-cleanup")
				return nil
			},
		},
		ProxyConfigWriter: func(cfg config.Config) (string, error) {
			record("write-proxy-config")
			return "/tmp/proxy.json", nil
		},
		Supervisor: supervisorStub{
			start: func(ctx context.Context, path string) error {
				record("start-proxy")
				started <- struct{}{}
				return nil
			},
			stop: func() error {
				record("stop-proxy")
				return nil
			},
			done: func() <-chan error {
				ch := make(chan error)
				return ch
			},
		},
		Prober: proberStub{
			check: func(context.Context) error {
				record("probe")
				return nil
			},
		},
		Status: status,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for supervisor start")
	}

	mu.Lock()
	gotPrefix := append([]string(nil), order...)
	gotReadyDuringRun := append([]bool(nil), readyStates...)
	mu.Unlock()

	wantPrefix := []string{"config", "load-state", "bootstrap", "save-state", "network-apply", "write-proxy-config", "start-proxy", "probe"}
	if len(gotPrefix) < len(wantPrefix) {
		t.Fatalf("expected at least %d steps, got %d (%v)", len(wantPrefix), len(gotPrefix), gotPrefix)
	}
	for i, want := range wantPrefix {
		if gotPrefix[i] != want {
			t.Fatalf("expected step %d to be %q, got %q (full order: %v)", i, want, gotPrefix[i], gotPrefix)
		}
	}

	if len(gotReadyDuringRun) == 0 || !gotReadyDuringRun[0] {
		t.Fatalf("expected app to mark ready after starting dependencies, got %v", gotReadyDuringRun)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for app shutdown")
	}

	mu.Lock()
	defer mu.Unlock()

	wantFinal := []string{
		"config",
		"load-state",
		"bootstrap",
		"save-state",
		"network-apply",
		"write-proxy-config",
		"start-proxy",
		"probe",
		"stop-proxy",
		"network-cleanup",
	}
	if len(order) != len(wantFinal) {
		t.Fatalf("expected final order %v, got %v", wantFinal, order)
	}
	for i, want := range wantFinal {
		if order[i] != want {
			t.Fatalf("expected final step %d to be %q, got %q (full order: %v)", i, want, order[i], order)
		}
	}

	if len(readyStates) != 2 || readyStates[0] != true || readyStates[1] != false {
		t.Fatalf("expected ready transitions [true false], got %v", readyStates)
	}
}

func TestApp_RunReturnsErrorWhenSupervisorExitsUnexpectedly(t *testing.T) {
	supervisorDone := make(chan error, 1)
	app, err := NewApp(Dependencies{
		ConfigLoader: func() (config.Config, error) {
			return config.Config{ProxyStack: config.StackDual}, nil
		},
		StateLoader: func() (state.State, error) {
			return state.State{SchemaVersion: 1}, nil
		},
		StateSaver: func(state.State) error {
			return nil
		},
		Bootstrapper: bootstrapperStub{
			ensureDevice: func(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
				return current, nil
			},
		},
		NetworkManager: networkManagerStub{
			apply: func(ctx context.Context, cfg config.Config, current state.State) error {
				return nil
			},
			cleanup: func(ctx context.Context) error {
				return nil
			},
		},
		ProxyConfigWriter: func(cfg config.Config) (string, error) {
			return "/tmp/proxy.json", nil
		},
		Supervisor: supervisorStub{
			start: func(ctx context.Context, path string) error {
				supervisorDone <- errors.New("proxy runtime crashed")
				return nil
			},
			stop: func() error {
				return nil
			},
			done: func() <-chan error {
				return supervisorDone
			},
		},
		Prober: proberStub{
			check: func(context.Context) error {
				return nil
			},
		},
		Status: &statusStub{setReady: func(bool) {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = app.Run(context.Background())
	if err == nil {
		t.Fatal("expected run to fail when supervisor exits unexpectedly")
	}
	if !strings.Contains(err.Error(), "proxy runtime crashed") {
		t.Fatalf("expected supervisor exit to surface, got %v", err)
	}
}

func TestApp_RunReturnsErrorWhenProbeFails(t *testing.T) {
	app, err := NewApp(Dependencies{
		ConfigLoader: func() (config.Config, error) {
			return config.Config{ProxyStack: config.StackDual}, nil
		},
		StateLoader: func() (state.State, error) {
			return state.State{SchemaVersion: 1}, nil
		},
		StateSaver: func(state.State) error {
			return nil
		},
		Bootstrapper: bootstrapperStub{
			ensureDevice: func(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
				return current, nil
			},
		},
		NetworkManager: networkManagerStub{
			apply: func(ctx context.Context, cfg config.Config, current state.State) error {
				return nil
			},
			cleanup: func(ctx context.Context) error {
				return nil
			},
		},
		ProxyConfigWriter: func(cfg config.Config) (string, error) {
			return "/tmp/proxy.json", nil
		},
		Supervisor: supervisorStub{
			start: func(ctx context.Context, path string) error {
				return nil
			},
			stop: func() error {
				return nil
			},
			done: func() <-chan error {
				ch := make(chan error)
				return ch
			},
		},
		Prober: proberStub{
			check: func(context.Context) error {
				return errors.New("warp probe failed")
			},
		},
		Status: &statusStub{setReady: func(bool) {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = app.Run(context.Background())
	if err == nil {
		t.Fatal("expected run to fail when probe fails")
	}
	if !strings.Contains(err.Error(), "warp probe failed") {
		t.Fatalf("expected probe failure to surface, got %v", err)
	}
}

func TestApp_RunRetriesWithFreshStateAfterProbeFailure(t *testing.T) {
	var (
		mu          sync.Mutex
		order       []string
		readyStates []bool
		starts      int
	)

	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}

	started := make(chan int, 2)

	app, err := NewApp(Dependencies{
		ConfigLoader: func() (config.Config, error) {
			record("config")
			return config.Config{ProxyStack: config.StackDual}, nil
		},
		StateLoader: func() (state.State, error) {
			record("load-state")
			return state.State{
				SchemaVersion: 1,
				DeviceID:      "stale-device",
				AccessToken:   "stale-token",
				PrivateKey:    "stale-private",
			}, nil
		},
		StateSaver: func(s state.State) error {
			record("save-state:" + s.DeviceID)
			return nil
		},
		Bootstrapper: bootstrapperStub{
			ensureDevice: func(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
				if current.DeviceID == "" {
					record("bootstrap:fresh")
					return state.State{
						SchemaVersion: 1,
						DeviceID:      "fresh-device",
						AccessToken:   "fresh-token",
						PrivateKey:    "fresh-private",
					}, nil
				}
				record("bootstrap:reuse")
				current.DeviceID = "stale-device"
				return current, nil
			},
		},
		NetworkManager: networkManagerStub{
			apply: func(ctx context.Context, cfg config.Config, current state.State) error {
				record("network-apply:" + current.DeviceID)
				return nil
			},
			cleanup: func(ctx context.Context) error {
				record("network-cleanup")
				return nil
			},
		},
		ProxyConfigWriter: func(cfg config.Config) (string, error) {
			record("write-proxy-config")
			return "/tmp/proxy.json", nil
		},
		Supervisor: supervisorStub{
			start: func(ctx context.Context, path string) error {
				mu.Lock()
				starts++
				currentStart := starts
				mu.Unlock()
				record("start-proxy")
				started <- currentStart
				return nil
			},
			stop: func() error {
				record("stop-proxy")
				return nil
			},
			done: func() <-chan error {
				ch := make(chan error)
				return ch
			},
		},
		Prober: proberStub{
			check: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				if starts == 1 {
					order = append(order, "probe:fail")
					return errors.New("stale state probe failed")
				}
				order = append(order, "probe:pass")
				return nil
			},
		},
		Status: &statusStub{
			setReady: func(ready bool) {
				mu.Lock()
				defer mu.Unlock()
				readyStates = append(readyStates, ready)
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for supervisor start")
		}
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown after recovery, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for app shutdown")
	}

	mu.Lock()
	defer mu.Unlock()

	wantOrder := []string{
		"config",
		"load-state",
		"bootstrap:reuse",
		"save-state:stale-device",
		"network-apply:stale-device",
		"write-proxy-config",
		"start-proxy",
		"probe:fail",
		"stop-proxy",
		"network-cleanup",
		"bootstrap:fresh",
		"save-state:fresh-device",
		"network-apply:fresh-device",
		"write-proxy-config",
		"start-proxy",
		"probe:pass",
		"stop-proxy",
		"network-cleanup",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("expected order %v, got %v", wantOrder, order)
	}
	for i, want := range wantOrder {
		if order[i] != want {
			t.Fatalf("expected order[%d]=%q, got %q (full order %v)", i, want, order[i], order)
		}
	}

	if len(readyStates) != 2 || readyStates[0] != true || readyStates[1] != false {
		t.Fatalf("expected ready transitions [true false], got %v", readyStates)
	}
}

type bootstrapperStub struct {
	ensureDevice func(context.Context, config.Config, state.State) (state.State, error)
}

func (b bootstrapperStub) EnsureDevice(ctx context.Context, cfg config.Config, current state.State) (state.State, error) {
	return b.ensureDevice(ctx, cfg, current)
}

type networkManagerStub struct {
	apply   func(context.Context, config.Config, state.State) error
	cleanup func(context.Context) error
}

func (n networkManagerStub) Apply(ctx context.Context, cfg config.Config, current state.State) error {
	return n.apply(ctx, cfg, current)
}

func (n networkManagerStub) Cleanup(ctx context.Context) error {
	return n.cleanup(ctx)
}

type supervisorStub struct {
	start func(context.Context, string) error
	stop  func() error
	done  func() <-chan error
}

func (s supervisorStub) Start(ctx context.Context, path string) error {
	return s.start(ctx, path)
}

func (s supervisorStub) Stop() error {
	return s.stop()
}

func (s supervisorStub) Done() <-chan error {
	return s.done()
}

type proberStub struct {
	check func(context.Context) error
}

func (p proberStub) Check(ctx context.Context) error {
	return p.check(ctx)
}

type statusStub struct {
	setReady func(bool)
}

func (s *statusStub) SetReady(ready bool) {
	s.setReady(ready)
}
