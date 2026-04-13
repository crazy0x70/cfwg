package router

import (
	"net/netip"
	"reflect"
	"testing"

	"cfwg/internal/config"
)

func TestBuildPlan_DefaultsToDualStackTunnelRoutes(t *testing.T) {
	got := BuildPlan(RouteInput{})

	want := []netip.Prefix{
		mustPrefix(t, "0.0.0.0/0"),
		mustPrefix(t, "::/0"),
	}

	if !reflect.DeepEqual(got.TunnelRoutes, want) {
		t.Fatalf("expected dual-stack tunnel routes, got %v", got.TunnelRoutes)
	}
}

func TestBuildPlan_IPv4OnlyTunnelRoutes(t *testing.T) {
	got := BuildPlan(RouteInput{StackMode: config.Stack4})

	want := []netip.Prefix{
		mustPrefix(t, "0.0.0.0/0"),
	}

	if !reflect.DeepEqual(got.TunnelRoutes, want) {
		t.Fatalf("expected ipv4-only tunnel routes, got %v", got.TunnelRoutes)
	}
}

func TestBuildPlan_IPv6OnlyTunnelRoutes(t *testing.T) {
	got := BuildPlan(RouteInput{StackMode: config.Stack6})

	want := []netip.Prefix{
		mustPrefix(t, "::/0"),
	}

	if !reflect.DeepEqual(got.TunnelRoutes, want) {
		t.Fatalf("expected ipv6-only tunnel routes, got %v", got.TunnelRoutes)
	}
}

func TestBuildPlan_AddsBypassRouteForPeerEndpoint(t *testing.T) {
	got := BuildPlan(RouteInput{
		StackMode:    config.Stack4,
		PeerEndpoint: mustAddr(t, "162.159.193.10"),
	})

	want := []netip.Prefix{
		mustPrefix(t, "162.159.193.10/32"),
	}

	if !reflect.DeepEqual(got.BypassRoutes, want) {
		t.Fatalf("expected endpoint bypass route, got %v", got.BypassRoutes)
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

func mustAddr(t *testing.T, raw string) netip.Addr {
	t.Helper()

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		t.Fatalf("parse addr %q: %v", raw, err)
	}

	return addr
}
