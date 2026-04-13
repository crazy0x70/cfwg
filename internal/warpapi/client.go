package warpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const registrationBasePath = "/v0a1922/reg"

var defaultHeaders = map[string]string{
	"User-Agent":        "okhttp/3.12.1",
	"CF-Client-Version": "a-6.3-1922",
}

type RegistrationInput struct {
	FCMToken  string `json:"fcm_token"`
	InstallID string `json:"install_id"`
	Key       string `json:"key"`
	Locale    string `json:"locale"`
	Model     string `json:"model"`
	TOS       string `json:"tos"`
	Type      string `json:"type"`
}

type Account struct {
	License string `json:"license"`
}

type Endpoint struct {
	Host string `json:"host"`
	V4   string `json:"v4"`
	V6   string `json:"v6"`
	Ports []int  `json:"ports"`
}

type Peer struct {
	Endpoint  Endpoint `json:"endpoint"`
	PublicKey string   `json:"public_key"`
}

type InterfaceAddresses struct {
	V4 string `json:"v4"`
	V6 string `json:"v6"`
}

type Interface struct {
	Addresses InterfaceAddresses `json:"addresses"`
}

type DeviceConfig struct {
	ClientID  string    `json:"client_id"`
	Interface Interface `json:"interface"`
	Peers     []Peer    `json:"peers"`
}

type Policy struct {
	TunnelProtocol string `json:"tunnel_protocol"`
}

type Device struct {
	ID          string       `json:"id"`
	Token       string       `json:"token"`
	Key         string       `json:"key"`
	KeyType     string       `json:"key_type"`
	TunnelType  string       `json:"tunnel_type"`
	Created     string       `json:"created"`
	Updated     string       `json:"updated"`
	WarpEnabled bool         `json:"warp_enabled"`
	Policy      Policy       `json:"policy"`
	Account     Account      `json:"account"`
	Config      DeviceConfig `json:"config"`
}

type LicenseUpdateInput struct {
	DeviceID string
	Token    string
	License  string
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
					MaxVersion: tls.VersionTLS12,
				},
				ForceAttemptHTTP2:     false,
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}

	return &Client{
		BaseURL: baseURL,
		HTTP:    httpClient,
	}
}

func (c *Client) Register(ctx context.Context, in RegistrationInput) (Device, error) {
	req, err := c.newJSONRequest(ctx, http.MethodPost, registrationBasePath, in)
	if err != nil {
		return Device{}, err
	}

	var device Device
	if err := c.doJSON(req, &device); err != nil {
		return Device{}, err
	}

	return device, nil
}

func (c *Client) GetSourceDevice(ctx context.Context, deviceID, token string) (Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+registrationBasePath+"/"+deviceID, nil)
	if err != nil {
		return Device{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	c.applyDefaultHeaders(req)

	var device Device
	if err := c.doJSON(req, &device); err != nil {
		return Device{}, err
	}

	return device, nil
}

func (c *Client) UpdateLicense(ctx context.Context, in LicenseUpdateInput) error {
	body := struct {
		License string `json:"license"`
	}{
		License: in.License,
	}

	req, err := c.newJSONRequest(ctx, http.MethodPut, registrationBasePath+"/"+in.DeviceID+"/account", body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+in.Token)

	return c.doJSON(req, nil)
}

func (c *Client) SetWarpEnabled(ctx context.Context, deviceID, token string, enabled bool) error {
	body := struct {
		WarpEnabled bool `json:"warp_enabled"`
	}{
		WarpEnabled: enabled,
	}

	req, err := c.newJSONRequest(ctx, http.MethodPatch, registrationBasePath+"/"+deviceID, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	return c.doJSON(req, nil)
}

func (c *Client) newJSONRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyDefaultHeaders(req)

	return req, nil
}

func (c *Client) applyDefaultHeaders(req *http.Request) {
	for key, value := range defaultHeaders {
		req.Header.Set(key, value)
	}
}

func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
