package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type httpProbeTarget struct {
	URL        string
	HostHeader string
}

type httpProber struct {
	URL             string
	HostHeader      string
	FallbackTargets []httpProbeTarget
	Client          *http.Client
	RetryWindow     time.Duration
	RetryInterval   time.Duration
}

func (p httpProber) Check(ctx context.Context) error {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	retryWindow := p.RetryWindow
	if retryWindow <= 0 {
		retryWindow = 30 * time.Second
	}
	retryInterval := p.RetryInterval
	if retryInterval <= 0 {
		retryInterval = 250 * time.Millisecond
	}

	deadline := time.Now().Add(retryWindow)
	var lastErr error
	for {
		lastErr = p.checkTargets(ctx, client)
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return lastErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

func (p httpProber) targets() []httpProbeTarget {
	targets := []httpProbeTarget{{
		URL:        p.URL,
		HostHeader: p.HostHeader,
	}}
	targets = append(targets, p.FallbackTargets...)

	filtered := targets[:0]
	for _, target := range targets {
		if target.URL == "" {
			continue
		}
		filtered = append(filtered, target)
	}

	return filtered
}

func (p httpProber) checkTargets(ctx context.Context, client *http.Client) error {
	targets := p.targets()
	if len(targets) == 0 {
		return errors.New("probe url is required")
	}

	var lastErr error
	for _, target := range targets {
		if err := p.checkOnce(ctx, client, target); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}

	return lastErr
}

func (p httpProber) checkOnce(ctx context.Context, client *http.Client, target httpProbeTarget) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return fmt.Errorf("create probe request: %w", err)
	}
	if target.HostHeader != "" {
		req.Host = target.HostHeader
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("perform probe request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read probe response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe returned status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "warp=on") {
		return fmt.Errorf("probe response does not confirm warp tunnel")
	}

	return nil
}
