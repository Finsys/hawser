package docker

import (
	"os"
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

	flags, fallback, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback != "" {
		t.Fatalf("expected no fallback path, got %q", fallback)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 rels, got %v", rels)
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

	flags, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
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

	flags, fallback, _, errMsg := resolveComposeFileFlags(stackDir, op)
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

func TestResolveComposeFileFlags_AutoDetectOmitsFForStandardRoot(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		Files: map[string]string{"compose.yaml": "services: {}"},
	}

	flags, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(flags) != 0 {
		t.Fatalf("expected no -f for auto-detected compose.yaml, got %v", flags)
	}
}

func TestResolveComposeFileFlags_AutoDetectMultipleStandardRootUsesF(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		Files: map[string]string{
			"docker-compose.yml": "services: {}",
			"compose.yaml":       "services: {}",
		},
	}

	flags, _, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(rels) != 1 || rels[0] != "docker-compose.yml" {
		t.Fatalf("expected historical candidate, got %v", rels)
	}
	if len(flags) != 2 || flags[0] != "-f" {
		t.Fatalf("expected explicit selected candidate, got %v", flags)
	}
}

func TestResolveComposeFileFlags_ContentFallbackUsesWrittenFile(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFile: "services: {}",
	}

	flags, fallback, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback == "" {
		t.Fatal("expected fallback write path")
	}
	if len(flags) != 2 || flags[0] != "-f" || !strings.HasSuffix(flags[1], "docker-compose.yml") {
		t.Fatalf("expected -f for written docker-compose.yml, got %v", flags)
	}
}

func TestResolveComposeFileFlags_NamedStandardFileUsesF(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"docker-compose.yml"},
		Files:            map[string]string{"docker-compose.yml": "services: {}", "compose.yaml": "services: {}"},
	}

	flags, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(flags) != 2 || !strings.HasSuffix(flags[1], "docker-compose.yml") {
		t.Fatalf("expected -f docker-compose.yml so Compose cannot prefer compose.yaml, got %v", flags)
	}
}

func TestResolveComposeFileFlags_DoesNotAppendDiskOverride(t *testing.T) {
	stackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stackDir, "compose.override.yaml"), []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}
	op := &ComposeOperation{
		ComposeFileNames: []string{"compose.yaml"},
		Files:            map[string]string{"compose.yaml": "services: {}"},
	}

	flags, _, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(rels) != 1 || rels[0] != "compose.yaml" {
		t.Fatalf("Dockhand list must be authoritative, got rels %v", rels)
	}
	if len(flags) != 2 || !strings.HasSuffix(flags[1], "compose.yaml") {
		t.Fatalf("expected only named compose.yaml, got %v", flags)
	}
}

func TestResolveComposeFileFlags_AutoDetectDoesNotListOverride(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		Files: map[string]string{
			"compose.yaml":          "services: {}",
			"compose.override.yaml": "services: {web: {}}",
		},
	}

	flags, _, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(rels) != 1 || rels[0] != "compose.yaml" {
		t.Fatalf("auto-detect should pick only the primary, got %v", rels)
	}
	if len(flags) != 0 {
		t.Fatalf("expected omit -f so Compose auto-discovers the override, got %v", flags)
	}
}

func TestResolveComposeFileFlags_NestedAutoDetect(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		Files: map[string]string{"apps/web/compose.yaml": "services: {}"},
	}

	flags, _, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(rels) != 1 || rels[0] != "apps/web/compose.yaml" {
		t.Fatalf("rels = %v", rels)
	}
	if len(flags) != 2 || !strings.HasSuffix(flags[1], filepath.FromSlash("apps/web/compose.yaml")) {
		t.Fatalf("expected nested auto-detect with -f, got %v", flags)
	}
}

func TestResolveComposeFileFlags_ComposeContentWinsNestedAutoDetect(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFile: "services: {}",
		Files:       map[string]string{"examples/compose.yaml": "services: {wrong: {}}"},
	}

	flags, fallback, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback == "" || len(rels) != 1 || rels[0] != "docker-compose.yml" {
		t.Fatalf("expected legacy content fallback, got flags=%v fallback=%q rels=%v", flags, fallback, rels)
	}
}

func TestResolveComposeFileFlags_TopLevelAutoDetectStillWinsComposeContent(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFile: "services: {legacy: {}}",
		Files:       map[string]string{"compose.yaml": "services: {selected: {}}"},
	}

	flags, fallback, rels, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if fallback != "" || len(rels) != 1 || rels[0] != "compose.yaml" || len(flags) != 0 {
		t.Fatalf("expected top-level auto-detection, got flags=%v fallback=%q rels=%v", flags, fallback, rels)
	}
}

func TestResolveComposeFileFlags_NonStandardNeedsF(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileName: "immich.yaml",
		Files:           map[string]string{"immich.yaml": "services: {}"},
	}

	flags, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if len(flags) != 2 || !strings.HasSuffix(flags[1], "immich.yaml") {
		t.Fatalf("expected -f for non-standard name, got %v", flags)
	}
}

func TestHasNamedComposeFiles_IgnoresStdinWhenNamesSet(t *testing.T) {
	if !hasNamedComposeFiles(&ComposeOperation{
		ComposeFile:      "services: {}",
		ComposeFileNames: []string{"compose.yaml"},
	}) {
		t.Fatal("composeFileNames must win over the legacy stdin path")
	}
	if hasNamedComposeFiles(&ComposeOperation{ComposeFile: "services: {}"}) {
		t.Fatal("compose content alone is not a named file list")
	}
}

func TestResolveComposeFileFlags_RejectsTraversal(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"../escape.yaml"},
	}

	_, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
	if errMsg == "" {
		t.Fatal("expected path traversal error")
	}
}

func TestResolveComposeFileFlags_RejectsEmptyName(t *testing.T) {
	stackDir := t.TempDir()
	op := &ComposeOperation{
		ComposeFileNames: []string{"compose.yaml", ""},
	}

	_, _, _, errMsg := resolveComposeFileFlags(stackDir, op)
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

func TestComposeFileAbsPath_RejectsSymlinkEscape(t *testing.T) {
	stackDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(stackDir, "link")); err != nil {
		t.Fatal(err)
	}
	_, errMsg := composeFileAbsPath(stackDir, "link/compose.yaml")
	if errMsg == "" {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestComposeProjectDir_NestedPrimary(t *testing.T) {
	stackDir := t.TempDir()
	got := composeProjectDir(stackDir, []string{"apps/web/compose.yaml"})
	want := filepath.Join(stackDir, "apps", "web")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestComposeProjectDir_RootPrimary(t *testing.T) {
	stackDir := t.TempDir()
	if got := composeProjectDir(stackDir, []string{"compose.yaml"}); got != stackDir {
		t.Fatalf("got %q want stack root", got)
	}
}
