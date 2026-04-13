package wgdev

import (
	"net/netip"
	"reflect"
	"testing"

	"cfwg/internal/config"
)

func TestDesiredConfig_AllowedIPsDefaultsToDualStack(t *testing.T) {
	got := DesiredConfig{}.AllowedIPs()

	want := []netip.Prefix{
		mustPrefix(t, "0.0.0.0/0"),
		mustPrefix(t, "::/0"),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected default dual-stack allowed IPs, got %v", got)
	}
}

func TestDesiredConfig_AllowedIPsIPv4Only(t *testing.T) {
	got := DesiredConfig{StackMode: config.Stack4}.AllowedIPs()

	want := []netip.Prefix{
		mustPrefix(t, "0.0.0.0/0"),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected ipv4-only allowed IPs, got %v", got)
	}
}

func TestDesiredConfig_AllowedIPsIPv6Only(t *testing.T) {
	got := DesiredConfig{StackMode: config.Stack6}.AllowedIPs()

	want := []netip.Prefix{
		mustPrefix(t, "::/0"),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected ipv6-only allowed IPs, got %v", got)
	}
}

func mustPrefix(t *testing.T, raw string) netip.Prefix {
	t.Helper()

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", raw, err)
	}

	return prefix
}
