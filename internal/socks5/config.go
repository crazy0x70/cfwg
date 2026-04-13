package socks5

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type FileConfig struct {
	ListenAddr         string      `json:"listen_addr"`
	PublicHost         string      `json:"public_host"`
	PublicPort         int         `json:"public_port,omitempty"`
	AllowUDP           bool        `json:"allow_udp"`
	UpstreamSOCKS5Addr string      `json:"upstream_socks5_addr,omitempty"`
	Auth               *AuthConfig `json:"auth,omitempty"`
}

func WriteFileConfig(path string, cfg FileConfig) error {
	if path == "" {
		return fmt.Errorf("socks5 config path is required")
	}
	if cfg.ListenAddr == "" {
		return fmt.Errorf("socks5 listen address is required")
	}
	if cfg.PublicHost == "" {
		cfg.PublicHost = "127.0.0.1"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal socks5 config: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o640); err != nil {
		return fmt.Errorf("write socks5 config: %w", err)
	}
	return nil
}

func LoadFileConfig(path string) (Config, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read socks5 config: %w", err)
	}

	var fileCfg FileConfig
	if err := json.Unmarshal(payload, &fileCfg); err != nil {
		return Config{}, fmt.Errorf("decode socks5 config: %w", err)
	}

	return Config{
		ListenAddr:         fileCfg.ListenAddr,
		PublicHost:         fileCfg.PublicHost,
		PublicPort:         fileCfg.PublicPort,
		AllowUDP:           fileCfg.AllowUDP,
		UpstreamSOCKS5Addr: fileCfg.UpstreamSOCKS5Addr,
		Auth:               fileCfg.Auth,
	}, nil
}

type Dialer struct {
	ServerAddr string
	Auth       *AuthConfig
	Timeout    time.Duration
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("unsupported network %q", network)
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var netDialer net.Dialer
	conn, err := netDialer.DialContext(ctx, "tcp", d.ServerAddr)
	if err != nil {
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := clientHandshake(conn, d.Auth); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := clientConnect(conn, address); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func clientHandshake(conn net.Conn, auth *AuthConfig) error {
	if auth == nil {
		if _, err := conn.Write([]byte{socksVersion, 0x01, authMethodNone}); err != nil {
			return err
		}
		reply, err := readExactly(conn, 2)
		if err != nil {
			return err
		}
		if reply[0] != socksVersion || reply[1] != authMethodNone {
			return fmt.Errorf("unexpected socks5 greeting reply %v", reply)
		}
		return nil
	}

	if _, err := conn.Write([]byte{socksVersion, 0x01, authMethodUserPass}); err != nil {
		return err
	}
	reply, err := readExactly(conn, 2)
	if err != nil {
		return err
	}
	if reply[0] != socksVersion || reply[1] != authMethodUserPass {
		return fmt.Errorf("unexpected auth greeting reply %v", reply)
	}

	user := []byte(auth.Username)
	pass := []byte(auth.Password)
	req := append([]byte{0x01, byte(len(user))}, user...)
	req = append(req, byte(len(pass)))
	req = append(req, pass...)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	authReply, err := readExactly(conn, 2)
	if err != nil {
		return err
	}
	if authReply[0] != 0x01 || authReply[1] != 0x00 {
		return fmt.Errorf("unexpected auth reply %v", authReply)
	}
	return nil
}

func clientConnect(conn net.Conn, address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}

	req := []byte{socksVersion, commandConnect, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			req = append(req, addrTypeIPv4)
			req = append(req, ipv4...)
		} else {
			req = append(req, addrTypeIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		req = append(req, addrTypeDomain, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, 0x00, 0x00)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}

	head, err := readExactly(conn, 4)
	if err != nil {
		return err
	}
	if head[0] != socksVersion || head[1] != replySucceeded {
		return fmt.Errorf("unexpected connect reply %v", head)
	}
	switch head[3] {
	case addrTypeIPv4:
		if _, err := readExactly(conn, 4); err != nil {
			return err
		}
	case addrTypeIPv6:
		if _, err := readExactly(conn, 16); err != nil {
			return err
		}
	case addrTypeDomain:
		size, err := readExactly(conn, 1)
		if err != nil {
			return err
		}
		if _, err := readExactly(conn, int(size[0])); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unexpected reply address type %d", head[3])
	}
	if _, err := readExactly(conn, 2); err != nil {
		return err
	}
	return nil
}
