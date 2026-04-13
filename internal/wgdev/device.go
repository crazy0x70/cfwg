package wgdev

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cfwg/internal/config"
	"cfwg/internal/system"
)

var (
	defaultRouteV4 = netip.MustParsePrefix("0.0.0.0/0")
	defaultRouteV6 = netip.MustParsePrefix("::/0")
)

const DefaultMTU = 1280

type DesiredConfig struct {
	Name          string
	StackMode     config.StackMode
	PrivateKey    string
	AddressV4     string
	AddressV6     string
	PeerPublicKey string
	Endpoint      string
	MTU           int
}

func (d DesiredConfig) AllowedIPs() []netip.Prefix {
	switch d.StackMode {
	case config.Stack4:
		return []netip.Prefix{defaultRouteV4}
	case config.Stack6:
		return []netip.Prefix{defaultRouteV6}
	default:
		return []netip.Prefix{defaultRouteV4, defaultRouteV6}
	}
}

func (d DesiredConfig) InterfaceAddresses() []string {
	switch d.StackMode {
	case config.Stack4:
		if d.AddressV4 == "" {
			return nil
		}
		return []string{d.AddressV4 + "/32"}
	case config.Stack6:
		if d.AddressV6 == "" {
			return nil
		}
		return []string{d.AddressV6 + "/128"}
	default:
		var addresses []string
		if d.AddressV4 != "" {
			addresses = append(addresses, d.AddressV4+"/32")
		}
		if d.AddressV6 != "" {
			addresses = append(addresses, d.AddressV6+"/128")
		}
		return addresses
	}
}

type Manager struct {
	Runner system.Runner
}

func (m Manager) Apply(ctx context.Context, cfg DesiredConfig) error {
	if m.Runner == nil {
		return fmt.Errorf("runner is required")
	}
	if err := validateDesiredConfig(cfg); err != nil {
		return err
	}

	if err := m.Delete(ctx, cfg.Name); err != nil {
		return err
	}

	if _, err := m.Runner.Run(ctx, "ip", "link", "add", "dev", cfg.Name, "type", "wireguard"); err != nil {
		return err
	}

	confPath, err := writeSetConf(cfg)
	if err != nil {
		return err
	}
	defer os.Remove(confPath)

	if _, err := m.Runner.Run(ctx, "wg", "setconf", cfg.Name, confPath); err != nil {
		return err
	}

	for _, address := range cfg.InterfaceAddresses() {
		if strings.Contains(address, ":") {
			if _, err := m.Runner.Run(ctx, "ip", "-6", "address", "add", address, "dev", cfg.Name); err != nil {
				return err
			}
			continue
		}

		if _, err := m.Runner.Run(ctx, "ip", "address", "add", address, "dev", cfg.Name); err != nil {
			return err
		}
	}

	mtu := cfg.MTU
	if mtu == 0 {
		mtu = DefaultMTU
	}

	if _, err := m.Runner.Run(ctx, "ip", "link", "set", "dev", cfg.Name, "mtu", strconv.Itoa(mtu), "up"); err != nil {
		return err
	}

	return nil
}

func (m Manager) Delete(ctx context.Context, name string) error {
	if m.Runner == nil {
		return fmt.Errorf("runner is required")
	}
	if name == "" {
		return fmt.Errorf("interface name is required")
	}

	if _, err := m.Runner.Run(ctx, "ip", "link", "del", "dev", name); err != nil {
		if strings.Contains(err.Error(), "Cannot find device") || strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return err
	}

	return nil
}

func validateDesiredConfig(cfg DesiredConfig) error {
	switch {
	case cfg.Name == "":
		return fmt.Errorf("interface name is required")
	case cfg.PrivateKey == "":
		return fmt.Errorf("private key is required")
	case cfg.PeerPublicKey == "":
		return fmt.Errorf("peer public key is required")
	case cfg.Endpoint == "":
		return fmt.Errorf("peer endpoint is required")
	case len(cfg.InterfaceAddresses()) == 0:
		return fmt.Errorf("at least one interface address is required")
	default:
		return nil
	}
}

func writeSetConf(cfg DesiredConfig) (string, error) {
	var allowedIPs []string
	for _, prefix := range cfg.AllowedIPs() {
		allowedIPs = append(allowedIPs, prefix.String())
	}

	content := fmt.Sprintf("[Interface]\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\n",
		cfg.PrivateKey,
		cfg.PeerPublicKey,
		strings.Join(allowedIPs, ", "),
		cfg.Endpoint,
	)

	file, err := os.CreateTemp("", filepath.Base(cfg.Name)+"-*.conf")
	if err != nil {
		return "", fmt.Errorf("create wireguard config: %w", err)
	}
	defer file.Close()

	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("chmod wireguard config: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		return "", fmt.Errorf("write wireguard config: %w", err)
	}

	return file.Name(), nil
}
