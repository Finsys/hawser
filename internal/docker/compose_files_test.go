package docker

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveComposeFileFlags_MultiFile(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"apps/web/compose.yaml", "apps/web/compose.override.yaml"},
		Files: map[string]string{
			"apps/web/compose.yaml":          "services: {}",
			"apps/web/compose.override.yaml": "services: {}",
		},
	}

	flags, fallback, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback != "" {
		t.Fatalf("expected no fallback path, got %q", fallback)
	}
	if len(flags) != 4 {
		t.Fatalf("expected 4 flag args (-f path x2), got %v", flags)
	}
	if flags[0] != "-f" || flags[2] != "-f" {
		t.Fatalf("expected alternating -f flags, got %v", flags)
	}
	if !strings.HasSuffix(flags[1], filepath.FromSlash("apps/web/compose.yaml")) {
		t.Fatalf("first -f path = %q", flags[1])
	}
	if !strings.HasSuffix(flags[3], filepath.FromSlash("apps/web/compose.override.yaml")) {
		t.Fatalf("second -f path = %q", flags[3])
	}
}

func TestResolveComposeFileFlags_PrefersNamesOverSingular(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileName:  "ignored.yaml",
		ComposeFileNames: []string{"compose.yaml", "extra.yaml"},
		Files:            map[string]string{"compose.yaml": "x", "extra.yaml": "y"},
	}

	flags, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(flags) != 4 {
		t.Fatalf("expected multi -f from ComposeFileNames, got %v", flags)
	}
	if strings.Contains(flags[1], "ignored.yaml") {
		t.Fatalf("ComposeFileName should be ignored when ComposeFileNames is set: %v", flags)
	}
}

func TestResolveComposeFileFlags_SingularFallback(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileName: "apps/api/compose.yaml",
	}

	flags, fallback, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback != "" {
		t.Fatalf("unexpected fallback %q", fallback)
	}
	if len(flags) != 2 || flags[0] != "-f" {
		t.Fatalf("expected single -f pair, got %v", flags)
	}
	if !strings.HasSuffix(flags[1], filepath.FromSlash("apps/api/compose.yaml")) {
		t.Fatalf("path = %q", flags[1])
	}
}

func TestResolveComposeFileFlags_AutoDetect(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		Files: map[string]string{"compose.yaml": "services: {}"},
	}

	flags, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(flags) != 2 || !strings.HasSuffix(flags[1], "compose.yaml") {
		t.Fatalf("expected auto-detect compose.yaml, got %v", flags)
	}
}

func TestResolveComposeFileFlags_ContentFallback(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFile: "services: {}",
	}

	flags, fallback, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback == "" {
		t.Fatal("expected fallback write path")
	}
	if len(flags) != 2 || flags[1] != fallback {
		t.Fatalf("flags=%v fallback=%q", flags, fallback)
	}
}

func TestResolveComposeFileFlags_RejectsTraversal(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"../escape.yaml"},
	}

	_, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg == "" {
		t.Fatal("expected path traversal error")
	}
}

func TestResolveComposeFileFlags_RejectsEmptyName(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"compose.yaml", ""},
	}

	_, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg == "" {
		t.Fatal("expected empty path error")
	}
}

func TestComposeFileAbsPath_SafeNested(t *testing.T) {
	stackDir := t.TempDir()
	abs, errMsg := composeFileAbsPath(stackDir, "apps/web/compose.yaml")
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !strings.HasPrefix(abs, stackDir) {
		t.Fatalf("abs path %q not under stackDir %q", abs, stackDir)
	}
}
