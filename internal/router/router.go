package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"cfwg/internal/config"
	"cfwg/internal/system"
)

var (
	defaultRouteV4 = netip.MustParsePrefix("0.0.0.0/0")
	defaultRouteV6 = netip.MustParsePrefix("::/0")
)

type RouteInput struct {
	StackMode    config.StackMode
	PeerEndpoint netip.Addr
}

type Plan struct {
	TunnelRoutes []netip.Prefix
	BypassRoutes []netip.Prefix
}

type defaultRoute struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

type Manager struct {
	Runner       system.Runner
	originalV4   *defaultRoute
	originalV6   *defaultRoute
	bypassRoutes []netip.Prefix
}

func BuildPlan(input RouteInput) Plan {
	plan := Plan{
		TunnelRoutes: tunnelRoutes(input.StackMode),
	}

	if !input.PeerEndpoint.IsValid() {
		return plan
	}

	endpoint := input.PeerEndpoint.Unmap()
	plan.BypassRoutes = []netip.Prefix{
		netip.PrefixFrom(endpoint, endpoint.BitLen()),
	}

	return plan
}

func (m *Manager) Apply(ctx context.Context, interfaceName string, plan Plan) error {
	if m.Runner == nil {
		return fmt.Errorf("runner is required")
	}
	if interfaceName == "" {
		return fmt.Errorf("interface name is required")
	}

	if m.originalV4 == nil {
		route, err := discoverDefaultRoute(ctx, m.Runner, false)
		if err != nil {
			return err
		}
		m.originalV4 = route
	}
	if m.originalV6 == nil {
		route, err := discoverDefaultRoute(ctx, m.Runner, true)
		if err != nil {
			return err
		}
		m.originalV6 = route
	}

	for _, bypass := range plan.BypassRoutes {
		if err := applyRoute(ctx, m.Runner, "replace", bypass, selectedOriginalRoute(bypass, m.originalV4, m.originalV6)); err != nil {
			return err
		}
	}
	m.bypassRoutes = append([]netip.Prefix(nil), plan.BypassRoutes...)

	for _, tunnelRoute := range plan.TunnelRoutes {
		if tunnelRoute.Addr().Is6() {
			if _, err := m.Runner.Run(ctx, "ip", "-6", "route", "replace", "default", "dev", interfaceName); err != nil {
				return err
			}
			continue
		}

		if _, err := m.Runner.Run(ctx, "ip", "route", "replace", "default", "dev", interfaceName); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) Cleanup(ctx context.Context) error {
	if m.Runner == nil {
		return fmt.Errorf("runner is required")
	}

	for _, bypass := range m.bypassRoutes {
		if err := deleteRoute(ctx, m.Runner, bypass); err != nil {
			return err
		}
	}
	m.bypassRoutes = nil

	if m.originalV4 != nil {
		if err := restoreDefaultRoute(ctx, m.Runner, false, m.originalV4); err != nil {
			return err
		}
	}
	if m.originalV6 != nil {
		if err := restoreDefaultRoute(ctx, m.Runner, true, m.originalV6); err != nil {
			return err
		}
	}

	return nil
}

func tunnelRoutes(mode config.StackMode) []netip.Prefix {
	switch mode {
	case config.Stack4:
		return []netip.Prefix{defaultRouteV4}
	case config.Stack6:
		return []netip.Prefix{defaultRouteV6}
	default:
		return []netip.Prefix{defaultRouteV4, defaultRouteV6}
	}
}

func discoverDefaultRoute(ctx context.Context, runner system.Runner, ipv6 bool) (*defaultRoute, error) {
	args := []string{"-j"}
	if ipv6 {
		args = append(args, "-6")
	}
	args = append(args, "route", "show", "default")

	output, err := runner.Run(ctx, "ip", args...)
	if err != nil {
		if ipv6 && strings.Contains(err.Error(), "ipv6") {
			return nil, nil
		}
		return nil, err
	}

	var routes []defaultRoute
	if err := json.Unmarshal(output, &routes); err != nil {
		return nil, fmt.Errorf("decode default routes: %w", err)
	}
	if len(routes) == 0 {
		return nil, nil
	}

	return &routes[0], nil
}

func applyRoute(ctx context.Context, runner system.Runner, action string, prefix netip.Prefix, original *defaultRoute) error {
	if original == nil {
		return fmt.Errorf("missing original route for %s", prefix)
	}

	args := []string{"route", action, prefix.String()}
	if prefix.Addr().Is6() {
		args = append([]string{"-6"}, args...)
	}
	if original.Gateway != "" {
		args = append(args, "via", original.Gateway)
	}
	if original.Dev != "" {
		args = append(args, "dev", original.Dev)
	}

	_, err := runner.Run(ctx, "ip", args...)
	return err
}

func deleteRoute(ctx context.Context, runner system.Runner, prefix netip.Prefix) error {
	args := []string{"route", "del", prefix.String()}
	if prefix.Addr().Is6() {
		args = append([]string{"-6"}, args...)
	}

	if _, err := runner.Run(ctx, "ip", args...); err != nil {
		if strings.Contains(err.Error(), "No such process") {
			return nil
		}
		return err
	}

	return nil
}

func restoreDefaultRoute(ctx context.Context, runner system.Runner, ipv6 bool, route *defaultRoute) error {
	args := []string{"route", "replace", "default"}
	if ipv6 {
		args = append([]string{"-6"}, args...)
	}
	if route.Gateway != "" {
		args = append(args, "via", route.Gateway)
	}
	if route.Dev != "" {
		args = append(args, "dev", route.Dev)
	}

	_, err := runner.Run(ctx, "ip", args...)
	return err
}

func selectedOriginalRoute(prefix netip.Prefix, v4, v6 *defaultRoute) *defaultRoute {
	if prefix.Addr().Is6() {
		return v6
	}

	return v4
}
