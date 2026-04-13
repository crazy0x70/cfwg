package config

import "testing"

func TestLoad_NormalizesMissingOptionalVariables(t *testing.T) {
	cfg, err := LoadFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ProxyStack != StackDual {
		t.Fatalf("expected dual stack, got %v", cfg.ProxyStack)
	}

	if cfg.WARPLicense != Unspecified {
		t.Fatalf("expected unspecified license, got %q", cfg.WARPLicense)
	}

	if cfg.Auth.Enabled {
		t.Fatal("expected anonymous auth by default")
	}
}

func TestLoad_RejectsHalfConfiguredAuth(t *testing.T) {
	_, err := LoadFromEnv(func(k string) string {
		if k == "uname" {
			return "demo"
		}

		return ""
	})
	if err == nil {
		t.Fatal("expected auth validation error")
	}
}
