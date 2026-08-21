package docker

import (
	"sort"
	"strings"
	"testing"
)

// TestBuildComposeEnv_InjectsSecrets verifies that resolved secret env vars
// (merged into envVars by Dockhand) are injected into the compose process env.
func TestBuildComposeEnv_InjectsSecrets(t *testing.T) {
	entries, err := buildComposeEnv(map[string]string{
		"SECRET_CHECK": "resolved-secret-value",
		"DB_PASSWORD":  "hunter2",
		"APP_ENV":      "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := envMap(entries)
	for k, want := range map[string]string{
		"SECRET_CHECK": "resolved-secret-value",
		"DB_PASSWORD":  "hunter2",
		"APP_ENV":      "production",
	} {
		if got[k] != want {
			t.Errorf("env %q = %q, want %q", k, got[k], want)
		}
	}
}

// TestBuildComposeEnv_StripsControlVars verifies that Dockhand control/selector
// variables are stripped and never injected (doc task 5: bulk-pull selector must
// not leak into the container), while sibling vars are still injected.
func TestBuildComposeEnv_StripsControlVars(t *testing.T) {
	entries, err := buildComposeEnv(map[string]string{
		"DOCKHAND_SECRET_SELECTOR": "prod/app",
		"OP_ENVIRONMENT_ID":        "env_123",
		"PULLED_KEY":               "pulled-value",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := envMap(entries)
	if _, ok := got["DOCKHAND_SECRET_SELECTOR"]; ok {
		t.Error("DOCKHAND_SECRET_SELECTOR was injected; it must be stripped")
	}
	if _, ok := got["OP_ENVIRONMENT_ID"]; ok {
		t.Error("OP_ENVIRONMENT_ID was injected; it must be stripped")
	}
	if got["PULLED_KEY"] != "pulled-value" {
		t.Errorf("PULLED_KEY = %q, want %q", got["PULLED_KEY"], "pulled-value")
	}
}

// TestBuildComposeEnv_StripsControlVarsCaseInsensitive verifies control-var
// stripping is case-insensitive (env keys can arrive in any case).
func TestBuildComposeEnv_StripsControlVarsCaseInsensitive(t *testing.T) {
	entries, err := buildComposeEnv(map[string]string{
		"dockhand_secret_selector": "prod/app",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected control var stripped, got entries: %v", entries)
	}
}

// TestBuildComposeEnv_BlocksDangerousKeys verifies code-execution / Docker-redirect
// vars are dropped rather than injected.
func TestBuildComposeEnv_BlocksDangerousKeys(t *testing.T) {
	for _, key := range []string{"LD_PRELOAD", "PATH", "DOCKER_HOST", "BASH_ENV"} {
		entries, err := buildComposeEnv(map[string]string{
			key:      "malicious",
			"SAFE_K": "ok",
		})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", key, err)
		}
		got := envMap(entries)
		if _, ok := got[key]; ok {
			t.Errorf("dangerous key %q was injected; it must be blocked", key)
		}
		if got["SAFE_K"] != "ok" {
			t.Errorf("%s: SAFE_K = %q, want ok", key, got["SAFE_K"])
		}
	}
}

// TestBuildComposeEnv_RejectsInvalidKeyNames verifies key-format validation rejects
// injection-style names, matching the previous inline behavior.
func TestBuildComposeEnv_RejectsInvalidKeyNames(t *testing.T) {
	for _, key := range []string{"BAD KEY", "KEY=INJECT", "9LEADING", "has-dash", ""} {
		_, err := buildComposeEnv(map[string]string{key: "v"})
		if err == nil {
			t.Errorf("expected error for invalid key %q, got nil", key)
		}
	}
}

// TestBuildComposeEnv_PreservesValueWithEquals verifies that values containing "="
// (common in tokens/base64 secrets) survive intact.
func TestBuildComposeEnv_PreservesValueWithEquals(t *testing.T) {
	entries, err := buildComposeEnv(map[string]string{
		"TOKEN": "abc=def==",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := envMap(entries)
	if got["TOKEN"] != "abc=def==" {
		t.Errorf("TOKEN = %q, want %q", got["TOKEN"], "abc=def==")
	}
}

// envMap splits "KEY=VALUE" entries into a map, splitting on the first '='.
func envMap(entries []string) map[string]string {
	m := make(map[string]string, len(entries))
	sort.Strings(entries)
	for _, e := range entries {
		k, v, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		m[k] = v
	}
	return m
}
