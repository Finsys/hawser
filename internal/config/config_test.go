package config

import (
	"os"
	"strings"
	"testing"
)

func TestIsLoopbackBind(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"127.0.0.5":   true,
		"::1":         true,
		"localhost":   true,
		"":            false, // empty == all interfaces
		"0.0.0.0":     false,
		"::":          false,
		"192.168.1.7": false,
		"example.com": false, // unresolvable hostname -> fail closed
	}
	for addr, want := range cases {
		if got := isLoopbackBind(addr); got != want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", addr, got, want)
		}
	}
}

// newStandardCfg builds a minimal standard-mode config. DockerHost is set so
// validate() skips the socket-exists check, isolating the auth/bind logic.
func newStandardCfg(bind, token string, allow bool) *Config {
	return &Config{
		Port:                2376,
		BindAddress:         bind,
		Token:               token,
		AllowInsecureNoAuth: allow,
		DockerHost:          "tcp://localhost:2375",
	}
}

func TestValidate_StandardMode_NoAuthBind(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{"non-loopback no token rejected", newStandardCfg("0.0.0.0", "", false), true},
		{"specific LAN ip no token rejected", newStandardCfg("192.168.1.7", "", false), true},
		{"empty bind no token rejected", newStandardCfg("", "", false), true},
		{"non-loopback with token allowed", newStandardCfg("0.0.0.0", "secret", false), false},
		{"loopback no token allowed", newStandardCfg("127.0.0.1", "", false), false},
		{"localhost no token allowed", newStandardCfg("localhost", "", false), false},
		{"non-loopback no token with override allowed", newStandardCfg("0.0.0.0", "", true), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Edge mode must not be affected by the standard-mode bind check.
func TestValidate_EdgeMode_Unaffected(t *testing.T) {
	cfg := &Config{
		DockhandServerURL: "wss://dockhand.example.com/api/hawser/connect",
		Token:             "secret",
		Port:              2376,
		BindAddress:       "0.0.0.0",
		DockerHost:        "tcp://localhost:2375",
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("edge-mode validate() unexpected error: %v", err)
	}
}

// TestLoad_ComposeTimeout verifies ComposeTimeout defaults to 900s and can be
// overridden via COMPOSE_TIMEOUT, mirroring how RequestTimeout already
// behaves. DOCKER_HOST is set so Load() skips the local docker-socket
// existence check, isolating the timeout-parsing behavior under test.
func TestLoad_ComposeTimeout(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://localhost:2375")
	t.Setenv("TOKEN", "test-token")

	t.Run("defaults to 900 when unset", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.ComposeTimeout != 900 {
			t.Errorf("ComposeTimeout = %d, want 900", cfg.ComposeTimeout)
		}
	})

	t.Run("overridden via COMPOSE_TIMEOUT", func(t *testing.T) {
		t.Setenv("COMPOSE_TIMEOUT", "120")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.ComposeTimeout != 120 {
			t.Errorf("ComposeTimeout = %d, want 120", cfg.ComposeTimeout)
		}
	})
}

// TestLoad_MaxMessageSize verifies MaxMessageSizeMB defaults to 256 and can be
// overridden via MAX_MESSAGE_SIZE_MB, so large git repos can deploy over edge (#1581).
func TestLoad_MaxMessageSize(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://localhost:2375")
	t.Setenv("TOKEN", "test-token")

	t.Run("defaults to 256 when unset", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.MaxMessageSizeMB != 256 {
			t.Errorf("MaxMessageSizeMB = %d, want 256", cfg.MaxMessageSizeMB)
		}
	})

	t.Run("overridden via MAX_MESSAGE_SIZE_MB", func(t *testing.T) {
		t.Setenv("MAX_MESSAGE_SIZE_MB", "512")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.MaxMessageSizeMB != 512 {
			t.Errorf("MaxMessageSizeMB = %d, want 512", cfg.MaxMessageSizeMB)
		}
	})
}

func TestLoad_Token(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://localhost:2375")

	t.Run("token loaded from TOKEN env var", func(t *testing.T) {
		t.Setenv("TOKEN", "env-secret-token")
		t.Setenv("TOKEN_FILE", "")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Token != "env-secret-token" {
			t.Errorf("Token = %q, want %q", cfg.Token, "env-secret-token")
		}
		if cfg.TokenFile != "" {
			t.Errorf("TokenFile = %q, want empty", cfg.TokenFile)
		}
	})

	t.Run("token loaded from TOKEN_FILE", func(t *testing.T) {
		tmpDir := t.TempDir()
		tokenPath := tmpDir + "/token"
		if err := os.WriteFile(tokenPath, []byte("file-secret-token\n"), 0o600); err != nil {
			t.Fatalf("failed writing token file: %v", err)
		}

		t.Setenv("TOKEN", "")
		t.Setenv("TOKEN_FILE", tokenPath)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Token != "file-secret-token" {
			t.Errorf("Token = %q, want %q", cfg.Token, "file-secret-token")
		}
		if cfg.TokenFile != tokenPath {
			t.Errorf("TokenFile = %q, want %q", cfg.TokenFile, tokenPath)
		}
	})

	t.Run("TOKEN_FILE takes precedence over TOKEN", func(t *testing.T) {
		tmpDir := t.TempDir()
		tokenPath := tmpDir + "/token"
		if err := os.WriteFile(tokenPath, []byte("file-precedence-token  \r\n"), 0o600); err != nil {
			t.Fatalf("failed writing token file: %v", err)
		}

		t.Setenv("TOKEN", "env-lower-precedence-token")
		t.Setenv("TOKEN_FILE", tokenPath)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Token != "file-precedence-token" {
			t.Errorf("Token = %q, want %q", cfg.Token, "file-precedence-token")
		}
	})

	t.Run("TOKEN_FILE not found returns error", func(t *testing.T) {
		t.Setenv("TOKEN_FILE", "/nonexistent/path/to/token")
		t.Setenv("TOKEN", "ignored-fallback-token")

		cfg, err := Load()
		if err == nil {
			t.Fatalf("expected error for nonexistent TOKEN_FILE, got cfg: %+v", cfg)
		}
		if !strings.Contains(err.Error(), "reading TOKEN_FILE") {
			t.Errorf("expected error mentioning 'reading TOKEN_FILE', got %v", err)
		}
	})
}
