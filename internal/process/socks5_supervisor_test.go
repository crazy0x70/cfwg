package process

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSOCKS5Supervisor_StartReturnsErrorWhenBinaryMissing(t *testing.T) {
	s := SOCKS5Supervisor{BinaryPath: "/missing/warp-socksd"}
	if err := s.Start(context.Background(), "/tmp/socks5.json"); err == nil {
		t.Fatal("expected start error")
	}
}

func TestSOCKS5Supervisor_StopTerminatesStartedProcess(t *testing.T) {
	binaryPath := writeFakeSOCKS5Binary(t)
	configPath := filepath.Join(t.TempDir(), "socks5.json")
	if err := os.WriteFile(configPath, []byte(`{"listen_addr":":1080"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s := SOCKS5Supervisor{BinaryPath: binaryPath}
	if err := s.Start(context.Background(), configPath); err != nil {
		t.Fatalf("expected start to succeed, got %v", err)
	}
	started := true
	t.Cleanup(func() {
		if started {
			_ = s.Stop()
		}
	})

	if err := s.Stop(); err != nil {
		t.Fatalf("expected stop to succeed, got %v", err)
	}
	started = false
}

func TestSOCKS5Supervisor_StartWaitsForListeningPort(t *testing.T) {
	address := "127.0.0.1:19093"
	binaryPath := writeDelayedSOCKS5ListenerBinary(t, address, 1200*time.Millisecond)
	configPath := filepath.Join(t.TempDir(), "socks5.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"listen_addr":"%s"}`, address)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s := SOCKS5Supervisor{BinaryPath: binaryPath}
	start := time.Now()
	if err := s.Start(context.Background(), configPath); err != nil {
		t.Fatalf("expected start to succeed, got %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("expected start to wait for delayed listener, returned in %s", elapsed)
	}

	conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("expected listener to be reachable once start returns, got %v", err)
	}
	_ = conn.Close()
}

func writeFakeSOCKS5Binary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-warp-socksd")
	script := `#!/bin/sh
if [ "$1" != "serve-socks5" ]; then exit 9; fi
config_path="$2"
host="$(python3 -c 'import json,sys; addr=json.load(open(sys.argv[1]))["listen_addr"]; host=addr.rsplit(":",1)[0]; print(host or "127.0.0.1")' "$config_path")"
port="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["listen_addr"].rsplit(":",1)[1])' "$config_path")"
exec python3 -m http.server "$port" --bind "$host" >/dev/null 2>&1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake socks5 binary: %v", err)
	}

	return path
}

func writeDelayedSOCKS5ListenerBinary(t *testing.T, address string, delay time.Duration) string {
	t.Helper()

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split delayed listener address: %v", err)
	}

	path := filepath.Join(t.TempDir(), "fake-delayed-warp-socksd")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" != "serve-socks5" ]; then exit 9; fi
sleep %.1f
exec python3 -m http.server %s --bind %s >/dev/null 2>&1
`, delay.Seconds(), port, host)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write delayed socks5 binary: %v", err)
	}

	return path
}
