package config

import "errors"

const Unspecified = "unspecified"

type StackMode string

const (
	StackDual StackMode = "dual"
	Stack4    StackMode = "4"
	Stack6    StackMode = "6"
)

type AuthConfig struct {
	Enabled  bool
	Username string
	Password string
}

type Config struct {
	ProxyStack  StackMode
	WARPLicense string
	Auth        AuthConfig
}

func LoadFromEnv(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("getenv is required")
	}

	proxyStack, err := loadStackMode(getenv("proxy-stack"))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ProxyStack:  proxyStack,
		WARPLicense: normalizeOptional(getenv("warp-license")),
	}

	username := normalizeOptional(getenv("uname"))
	password := normalizeOptional(getenv("upwd"))

	switch {
	case username == Unspecified && password == Unspecified:
		cfg.Auth = AuthConfig{
			Username: username,
			Password: password,
		}
	case username == Unspecified || password == Unspecified:
		return Config{}, errors.New("uname and upwd must be configured together")
	default:
		cfg.Auth = AuthConfig{
			Enabled:  true,
			Username: username,
			Password: password,
		}
	}

	return cfg, nil
}

func loadStackMode(value string) (StackMode, error) {
	switch normalizeOptional(value) {
	case Unspecified:
		return StackDual, nil
	case string(Stack4):
		return Stack4, nil
	case string(Stack6):
		return Stack6, nil
	default:
		return "", errors.New("proxy-stack must be 4, 6, or unspecified")
	}
}

func normalizeOptional(value string) string {
	if value == "" {
		return Unspecified
	}

	return value
}
