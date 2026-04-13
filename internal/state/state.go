package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const stateFileName = "state.json"

type State struct {
	SchemaVersion  int    `json:"schema_version"`
	DeviceID       string `json:"device_id,omitempty"`
	AccessToken    string `json:"access_token,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	IPv4           string `json:"v4,omitempty"`
	IPv6           string `json:"v6,omitempty"`
	PeerPublicKey  string `json:"peer_public_key,omitempty"`
	PeerEndpoint   string `json:"peer_endpoint,omitempty"`
	PeerEndpointV4 string `json:"peer_endpoint_v4,omitempty"`
	PeerEndpointV6 string `json:"peer_endpoint_v6,omitempty"`
	License        string `json:"license,omitempty"`
	WarpEnabled    bool   `json:"warp_enabled,omitempty"`
	TunnelProtocol string `json:"tunnel_protocol,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type Store struct {
	dir string
}

func NewStore(dir string) Store {
	return Store{dir: dir}
}

func (s Store) Load() (State, error) {
	path := filepath.Join(s.dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}

		return State{}, fmt.Errorf("read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode state file: %w", err)
	}

	return state, nil
}

func (s Store) Save(state State) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, "state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}

	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp state file: %w", err)
	}

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp state file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}

	path := filepath.Join(s.dir, stateFileName)
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod state file: %w", err)
	}

	cleanup = false
	return nil
}
