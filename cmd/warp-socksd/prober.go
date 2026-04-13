package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type httpProber struct {
	URL           string
	HostHeader    string
	Client        *http.Client
	RetryWindow   time.Duration
	RetryInterval time.Duration
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
		lastErr = p.checkOnce(ctx, client)
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

func (p httpProber) checkOnce(ctx context.Context, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return fmt.Errorf("create probe request: %w", err)
	}
	if p.HostHeader != "" {
		req.Host = p.HostHeader
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
