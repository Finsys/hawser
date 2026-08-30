package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestExecuteEmitsLines verifies that Execute reports complete lines through
// the onLine callback as they arrive. "ps" with no compose file present is a
// harmless, deterministic operation: it never touches Docker state and always
// fails fast with "no configuration file provided: not found" on stderr.
func TestExecuteEmitsLines(t *testing.T) {
	var lines []string
	c := NewComposeClient("", t.TempDir())
	op := &ComposeOperation{Operation: "ps"}
	_, err := c.Execute(context.Background(), op, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("no lines emitted")
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		return // at least one non-empty line -- that is the assertion
	}
	t.Fatal("only empty lines emitted")
}

// TestOutputFallsBackToStderr verifies that Execute falls back to stderr for
// result.Output when stdout is empty. "ps" without a compose file writes
// exclusively to stderr (verified manually: stdout is empty, stderr carries
// "no configuration file provided: not found"), so this is a real run that
// exercises the fallback without needing an invented test helper.
func TestOutputFallsBackToStderr(t *testing.T) {
	c := NewComposeClient("", t.TempDir())
	res, err := c.Execute(context.Background(), &ComposeOperation{Operation: "ps"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "no configuration file provided") {
		t.Fatalf("Output = %q, want it to contain the stderr message (fallback did not apply)", res.Output)
	}
}

// TestExecuteEmitsBuildOutputFromStdout verifies that Execute captures build
// output specifically, because that is the one case where hooking stderr
// alone (the naive reading of "compose writes progress to stderr") silently
// drops the bulk of the output: a prior measurement found that WITHOUT a
// build compose writes to stderr only, but WITH a build the build log lands
// on stdout. TestExecuteEmitsLines alone cannot catch a regression that
// forgets to wire cmd.Stdout -- this test builds a trivial one-line image and
// checks that a marker written by the build reaches onLine.
//
// Gated behind HAWSER_TEST_DOCKER: this is the only test in the repo that
// needs Docker to actually build an image rather than just fail fast on its
// own, and this package's other tests intentionally have no external
// dependency. Running it unconditionally would make it the first test in an
// external contributor's CI run to spin up a real image build -- a fair
// objection from a maintainer who wasn't expecting that. Set
// HAWSER_TEST_DOCKER=1 to run it locally; it also skips (rather than fails)
// if Docker turns out to be unavailable or unreachable even with the switch
// set.
func TestExecuteEmitsBuildOutputFromStdout(t *testing.T) {
	if os.Getenv("HAWSER_TEST_DOCKER") == "" {
		t.Skip("set HAWSER_TEST_DOCKER=1 to run tests that build images with docker compose")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}

	const marker = "HAWSER_BUILD_OUTPUT_MARKER"
	project := fmt.Sprintf("hawsertest%d", time.Now().UnixNano())

	c := NewComposeClient("/var/run/docker.sock", t.TempDir())
	op := &ComposeOperation{
		Operation:   "up",
		ProjectName: project,
		Build:       true,
		Files: map[string]string{
			"docker-compose.yml": "services:\n  app:\n    build: .\n    command: [\"true\"]\n",
			"Dockerfile":         "FROM alpine:3.20\nRUN echo " + marker + "\n",
		},
	}

	t.Cleanup(func() {
		_, _ = c.Execute(context.Background(), &ComposeOperation{
			Operation:   "down",
			ProjectName: project,
		}, nil)
		_ = exec.Command("docker", "rmi", project+"-app:latest").Run()
	})

	var mu sync.Mutex
	var lines []string
	res, err := c.Execute(context.Background(), op, func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("compose up --build failed: %s", res.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, marker) {
			return // found on some line -- the assertion
		}
	}
	t.Fatalf("build marker %q not seen in %d emitted lines -- stdout not wired to onLine?", marker, len(lines))
}

// TestTeeLinesFlushesFinalLineWithoutNewline verifies teeLines' closer
// contract directly: the scanner only flushes an unterminated final line on
// EOF, so the returned close() must trigger that EOF (by closing the pipe's
// write end) before Execute reads the result. Without it, the last line of
// output -- often the most important one, e.g. a build's final status line
// -- would never reach onLine.
func TestTeeLinesFlushesFinalLineWithoutNewline(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var lines []string

	w, closeFn := teeLines(&buf, func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	})

	if _, err := w.Write([]byte("first line\nlast line without newline")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeFn()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"first line", "last line without newline"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	if buf.String() != "first line\nlast line without newline" {
		t.Fatalf("buf = %q, teeLines must still write dst (Execute's result buffer)", buf.String())
	}
}
