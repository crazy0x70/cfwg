package process

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"cfwg/internal/socks5"
)

type SOCKS5Supervisor struct {
	BinaryPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan error
	stopped bool
}

func (s *SOCKS5Supervisor) Start(ctx context.Context, configPath string) error {
	if s.BinaryPath == "" {
		return errors.New("socks5 binary path is required")
	}
	if configPath == "" {
		return errors.New("socks5 config path is required")
	}
	cfg, err := socks5.LoadFileConfig(configPath)
	if err != nil {
		return fmt.Errorf("load socks5 config: %w", err)
	}
	readinessAddr, err := localReadinessAddress(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("resolve socks5 readiness address: %w", err)
	}

	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return errors.New("socks5 process already running")
	}

	cmd := exec.CommandContext(ctx, s.BinaryPath, "serve-socks5", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	s.cmd = cmd
	s.stopped = false
	s.done = make(chan error, 1)
	done := s.done
	s.mu.Unlock()

	go s.wait(cmd, done)
	if err := waitForTCPReady(ctx, done, readinessAddr, 5*time.Second); err != nil {
		_ = s.Stop()
		return fmt.Errorf("wait for socks5 readiness: %w", err)
	}
	return nil
}

func (s *SOCKS5Supervisor) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	s.stopped = true
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	select {
	case err := <-done:
		return normalizeWaitError(err, true)
	case <-time.After(3 * time.Second):
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	return normalizeWaitError(<-done, true)
}

func (s *SOCKS5Supervisor) Done() <-chan error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.done == nil {
		ch := make(chan error, 1)
		close(ch)
		return ch
	}
	return s.done
}

func (s *SOCKS5Supervisor) wait(cmd *exec.Cmd, done chan error) {
	err := cmd.Wait()

	s.mu.Lock()
	stopped := s.stopped
	if s.cmd == cmd {
		s.cmd = nil
	}
	s.mu.Unlock()

	done <- normalizeWaitError(err, stopped)
	close(done)
}

func waitForTCPReady(ctx context.Context, done <-chan error, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		select {
		case err := <-done:
			if err != nil {
				return err
			}
			return errors.New("socks5 process exited before becoming ready")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", address)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func localReadinessAddress(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
