package docker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// clearManagedEnvFiles removes a managed env file that is no longer sent, so compose
// stops reading a stale plaintext value from the host.
func TestClearManagedEnvFilesRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("MP_UI_AUTH=plain\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed := clearManagedEnvFiles(dir, []string{".env"}, map[string]string{"compose.yaml": "x"})
	if !slices.Contains(removed, ".env") {
		t.Fatalf("expected .env removed, got %v", removed)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatalf("expected .env gone, stat err = %v", err)
	}
}

// A file being written this deploy (present in keepFiles) is kept, not removed.
func TestClearManagedEnvFilesKeepsFileBeingWritten(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("FOO=bar\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed := clearManagedEnvFiles(dir, []string{".env"}, map[string]string{".env": "FOO=bar\n"})
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed, got %v", removed)
	}
	if _, err := os.Stat(env); err != nil {
		t.Fatalf("expected .env still present, err = %v", err)
	}
}

// Only the managed-env allow-list is touched; a request to clear anything else is ignored.
func TestClearManagedEnvFilesIgnoresNonAllowlisted(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"compose.yaml", "docker-compose.yml", "secrets.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	removed := clearManagedEnvFiles(dir, []string{"compose.yaml", "docker-compose.yml", "secrets.txt"}, nil)
	if len(removed) != 0 {
		t.Fatalf("expected nothing removed, got %v", removed)
	}
	for _, name := range []string{"compose.yaml", "docker-compose.yml", "secrets.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s untouched, err = %v", name, err)
		}
	}
}

// Idempotent: clearing an already-absent file is a silent no-op.
func TestClearManagedEnvFilesAbsentIsNoop(t *testing.T) {
	dir := t.TempDir()
	if removed := clearManagedEnvFiles(dir, []string{".env", ".env.dockhand"}, nil); len(removed) != 0 {
		t.Fatalf("expected nothing removed for absent files, got %v", removed)
	}
}

// Containment: an escaping path is rejected; a managed name the user replaced with a
// symlink is left alone (not a regular file).
func TestClearManagedEnvFilesContainmentAndSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside-env")
	if err := os.WriteFile(outside, []byte("secret\n"), 0644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	clearManagedEnvFiles(stackDir, []string{"../outside-env"}, nil)
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("containment breach: outside file touched, err = %v", err)
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(dir, "real-env")
		if err := os.WriteFile(target, []byte("real\n"), 0644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		link := filepath.Join(stackDir, ".env")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if removed := clearManagedEnvFiles(stackDir, []string{".env"}, nil); len(removed) != 0 {
			t.Fatalf("expected symlinked .env left alone, got %v", removed)
		}
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("expected symlink still present, err = %v", err)
		}
	}
}

// Forward compatibility: a new Dockhand sends clearEnvFiles; an older agent must ignore an
// unknown field, not error. Pins that the compose op decodes WITHOUT DisallowUnknownFields.
func TestComposeOperationIgnoresUnknownFields(t *testing.T) {
	payload := []byte(`{"operation":"up","projectName":"x","someFutureField":["a"],"clearEnvFiles":[".env"]}`)
	var op ComposeOperation
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&op); err != nil {
		t.Fatalf("decode must ignore unknown fields, got error: %v", err)
	}
	if op.Operation != "up" || op.ProjectName != "x" {
		t.Fatalf("known fields must still decode, got op=%q project=%q", op.Operation, op.ProjectName)
	}
	if len(op.ClearEnvFiles) != 1 || op.ClearEnvFiles[0] != ".env" {
		t.Fatalf("clearEnvFiles should decode, got %v", op.ClearEnvFiles)
	}
}
