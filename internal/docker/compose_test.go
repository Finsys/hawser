package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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

// TestTeeLinesHandlesHugeLineWithoutDeadlock guards the fix for the >1MB
// unterminated-line deadlock: a bufio.Scanner capped at 1MB stops reading such
// a line, leaving the pipe unread and blocking every further write to it. With
// bufio.Reader the whole line is delivered and close() returns promptly.
func TestTeeLinesHandlesHugeLineWithoutDeadlock(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var got []string
	w, closeFn := teeLines(&buf, func(l string) {
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	})

	huge := strings.Repeat("x", 2*1024*1024) // 2MB, no newline
	payload := huge + "\nEND\n"

	// The write itself blocks against the unfixed teeLines (the reader goroutine
	// exits on ErrTooLong, so the pipe has no reader), so it MUST run off the
	// test goroutine -- otherwise a broken teeLines hangs here until the whole
	// package's test timeout rather than failing this test cleanly. Both the
	// write and the close have to complete within the deadline.
	writeDone := make(chan error, 1)
	go func() {
		if _, err := io.WriteString(w, payload); err != nil {
			writeDone <- err
			return
		}
		closeFn() // flushes the trailing "END" and joins the reader goroutine
		writeDone <- nil
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write/close on a >1MB line did not complete in 5s -- teeLines deadlocked (scanner-cap regression)")
	}

	// Assert on the delivered content, not merely on "no deadlock": a >1MB line
	// must reach onLine WHOLE, the trailing short line must follow, and dst (the
	// buffer Execute returns as result.Output) must hold the raw bytes. The
	// unfixed teeLines cannot satisfy these even if a harness happened to drain
	// the pipe -- the huge line never reaches the capped scanner.
	mu.Lock()
	defer mu.Unlock()
	want := []string{huge, "END"}
	if !reflect.DeepEqual(got, want) {
		desc := make([]string, len(got))
		for i, l := range got {
			desc[i] = fmt.Sprintf("%d bytes", len(l))
		}
		t.Fatalf("lines = %v, want [2MB line, \"END\"]", desc)
	}
	if buf.String() != payload {
		t.Fatalf("dst buffer = %d bytes, want the full %d bytes written", buf.Len(), len(payload))
	}
}

// TestBuildOperationArgs runs the build operation with `echo` standing in for the
// compose binary, so the output is the exact argument list compose would get.
// `docker compose build --pull` is what refreshes a Dockerfile's FROM image; `up
// --build --pull always` does not (measured on compose 5.1.3).
func TestBuildOperationArgs(t *testing.T) {
	cases := []struct {
		op   ComposeOperation
		want string
	}{
		{ComposeOperation{Operation: "build"}, "build"},
		{ComposeOperation{Operation: "build", PullBase: true}, "build --pull"},
		{ComposeOperation{Operation: "build", NoBuildCache: true}, "build --no-cache"},
		{ComposeOperation{Operation: "build", PullBase: true, NoBuildCache: true}, "build --pull --no-cache"},
	}
	for _, tc := range cases {
		c := NewComposeClient("", t.TempDir())
		c.composeCmd, c.composeArgs, c.composeChecked = "echo", nil, true
		op := tc.op
		res, err := c.Execute(context.Background(), &op, nil)
		if err != nil {
			t.Fatalf("%+v: Execute: %v", tc.op, err)
		}
		if got := strings.TrimSpace(res.Output); !strings.HasSuffix(got, tc.want) || strings.Contains(strings.TrimSuffix(got, tc.want), "build") {
			t.Fatalf("%+v: args = %q, want them to end with %q", tc.op, got, tc.want)
		}
	}
}

func TestInheritedEnv(t *testing.T) {
	host := map[string]string{
		"PATH":          "/usr/bin",
		"USER":          "root",
		"DOCKER_CONFIG": "/run/hawser",
		"TMPDIR":        "/run/hawser",
		"LD_PRELOAD":    "/evil.so",
		"DOCKER_HOST":   "tcp://elsewhere:2375",
	}
	lookup := func(k string) (string, bool) { v, ok := host[k]; return v, ok }

	got := inheritedEnv(lookup)
	want := []string{"PATH=/usr/bin", "USER=root", "DOCKER_CONFIG=/run/hawser", "TMPDIR=/run/hawser"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inheritedEnv = %v, want %v", got, want)
	}

	// The released unit sets no HOME; compose must not get one invented for it, or
	// ${HOME} in a compose file would resolve to a different path.
	for _, kv := range got {
		if strings.HasPrefix(kv, "HOME=") {
			t.Fatalf("HOME passed although the host has none: %v", got)
		}
	}
}

// TestExecutePassesInheritedEnv runs Execute against a stand-in compose that prints
// its environment, so it checks the env the real subprocess gets, not only the helper.
func TestExecutePassesInheritedEnv(t *testing.T) {
	fake := t.TempDir() + "/compose"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", "/run/hawser")
	t.Setenv("TMPDIR", "/run/hawser")
	t.Setenv("LD_PRELOAD", "/evil.so")

	c := &ComposeClient{composeCmd: fake, composeChecked: true, stacksDir: t.TempDir()}
	res, err := c.Execute(context.Background(), &ComposeOperation{Operation: "ps"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	env := strings.Split(res.Output, "\n")
	for _, want := range []string{"DOCKER_CONFIG=/run/hawser", "TMPDIR=/run/hawser"} {
		if !slices.Contains(env, want) {
			t.Errorf("compose env lacks %s: %q", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "LD_PRELOAD") {
		t.Errorf("compose env carries LD_PRELOAD: %q", res.Output)
	}
}

// fakeCompose returns a client whose compose command appends its arguments to a log file.
func fakeCompose(t *testing.T, exitCode int) (*ComposeClient, string) {
	t.Helper()
	dir := t.TempDir()
	logFile := dir + "/args.log"
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %s\necho \"ran $*\"\nexit %d\n", logFile, exitCode)
	if err := os.WriteFile(dir+"/compose", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ComposeClient{composeCmd: dir + "/compose", composeChecked: true, stacksDir: t.TempDir()}, logFile
}

func readCalls(t *testing.T, logFile string) []string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// docker compose up rejects --no-cache, so a no-cache rebuild must be a separate build.
// The up keeps --build: without it `--pull always` replaces a service's fresh image with
// the registry tag named in its image: field (measured with docker compose 5.1).
func TestUpWithNoBuildCacheBuildsFirst(t *testing.T) {
	c, logFile := fakeCompose(t, 0)
	op := &ComposeOperation{Operation: "up", ProjectName: "p", ComposeFile: "services: {}", Build: true, NoBuildCache: true, PullPolicy: "always"}
	res, err := c.Execute(context.Background(), op, nil)
	if err != nil || !res.Success {
		t.Fatalf("Execute: %v %+v", err, res)
	}
	calls := readCalls(t, logFile)
	if len(calls) != 2 {
		t.Fatalf("want a build then an up, got %q", calls)
	}
	if !strings.Contains(calls[0], " build --no-cache") {
		t.Errorf("first call should be build --no-cache: %q", calls[0])
	}
	if !strings.Contains(calls[1], " up -d --remove-orphans --build --pull always") || strings.Contains(calls[1], "--no-cache") {
		t.Errorf("second call should be up --build with the pull policy and no --no-cache: %q", calls[1])
	}
	if !strings.Contains(res.Output, "ran "+calls[0]) || !strings.Contains(res.Output, "ran "+calls[1]) {
		t.Errorf("output should hold both steps: %q", res.Output)
	}
}

// File deletions run with the build step, before the build reads the context, and are
// reported in the result.
func TestUpWithNoBuildCacheDeletesBeforeBuild(t *testing.T) {
	dir := t.TempDir()
	stacks := t.TempDir()
	logFile := dir + "/args.log"
	stale := filepath.Join(stacks, "p", "stale.conf")
	script := fmt.Sprintf("#!/bin/sh\nif [ -e %s ]; then s=present; else s=absent; fi\necho \"$* stale=$s\" >> %s\n", stale, logFile)
	if err := os.WriteFile(dir+"/compose", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("old\n")
	if err := os.WriteFile(stale, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	c := &ComposeClient{composeCmd: dir + "/compose", composeChecked: true, stacksDir: stacks}
	op := &ComposeOperation{
		Operation: "up", ProjectName: "p", Build: true, NoBuildCache: true,
		Files:         map[string]string{"compose.yaml": "services: {}"},
		FilesToDelete: []FileToDelete{{Path: "stale.conf", Sha256: hex.EncodeToString(sum[:])}},
	}
	res, err := c.Execute(context.Background(), op, nil)
	if err != nil || !res.Success {
		t.Fatalf("Execute: %v %+v", err, res)
	}
	calls := readCalls(t, logFile)
	if len(calls) != 2 || !strings.Contains(calls[0], " build --no-cache") || !strings.HasSuffix(calls[0], "stale=absent") {
		t.Fatalf("the file must be gone before the build: %q", calls)
	}
	if len(res.DeletedFiles) != 1 || res.DeletedFiles[0] != "stale.conf" {
		t.Errorf("DeletedFiles = %q, want [stale.conf]", res.DeletedFiles)
	}
}

func TestUpWithNoBuildCacheStopsWhenBuildFails(t *testing.T) {
	c, logFile := fakeCompose(t, 1)
	op := &ComposeOperation{Operation: "up", ProjectName: "p", ComposeFile: "services: {}", Build: true, NoBuildCache: true}
	res, err := c.Execute(context.Background(), op, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("a failed build must fail the deploy")
	}
	if calls := readCalls(t, logFile); len(calls) != 1 || !strings.Contains(calls[0], " build --no-cache") {
		t.Fatalf("up must not run after a failed build: %q", calls)
	}
}

func TestUpNeverPassesNoCache(t *testing.T) {
	c, logFile := fakeCompose(t, 0)
	op := &ComposeOperation{Operation: "up", ProjectName: "p", ComposeFile: "services: {}", NoBuildCache: true}
	if _, err := c.Execute(context.Background(), op, nil); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, logFile); len(calls) != 1 || strings.Contains(calls[0], "--no-cache") {
		t.Fatalf("up without build: one call, no --no-cache: %q", calls)
	}
}

// writeStackFiles must preserve the executable bit for files that carry one
// (git stacks over Hawser used to always write 0644, breaking COPY+RUN scripts).
func TestWriteStackFilesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"script.sh":    "#!/bin/sh\necho hi\n",
		"readme.txt":   "plain",
		"nested/tool":  "#!/bin/sh\n",
		"bin/data.bin": "base64:" + base64.StdEncoding.EncodeToString([]byte{0, 1, 2}),
	}
	modes := map[string]uint32{
		"script.sh":   0755,
		"nested/tool": 0755,
		// readme.txt and bin/data.bin have no entry -> must be 0644.
	}
	if err := writeStackFiles(dir, files, modes); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}

	check := func(rel string, want os.FileMode) {
		t.Helper()
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode = %o, want %o", rel, got, want)
		}
	}
	check("script.sh", 0755)
	check("nested/tool", 0755)
	check("readme.txt", 0644)
	check("bin/data.bin", 0644)

	// Binary content survived the base64 round-trip.
	got, _ := os.ReadFile(filepath.Join(dir, "bin/data.bin"))
	if !bytes.Equal(got, []byte{0, 1, 2}) {
		t.Errorf("bin/data.bin content = %v, want [0 1 2]", got)
	}
}

// A nil modes map (an older Dockhand that sends no fileModes) must reproduce the
// pre-feature behavior: every file 0644, nothing executable.
func TestWriteStackFilesNilModesIs0644(t *testing.T) {
	dir := t.TempDir()
	if err := writeStackFiles(dir, map[string]string{"a.sh": "x", "b": "y"}, nil); err != nil {
		t.Fatalf("writeStackFiles: %v", err)
	}
	for _, rel := range []string{"a.sh", "b"} {
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if got := fi.Mode().Perm(); got != 0644 {
			t.Errorf("%s: mode = %o, want 0644 (nil modes must not set exec bit)", rel, got)
		}
	}
}

// On a re-deploy the target file already exists; os.WriteFile keeps the old mode
// on an existing file, so without the explicit Chmod the exec bit would be lost.
func TestWriteStackFilesRedeployUpdatesMode(t *testing.T) {
	dir := t.TempDir()
	// First deploy: not executable.
	if err := writeStackFiles(dir, map[string]string{"s.sh": "v1"}, nil); err != nil {
		t.Fatalf("first writeStackFiles: %v", err)
	}
	if got := statPerm(t, dir, "s.sh"); got != 0644 {
		t.Fatalf("after first deploy mode = %o, want 0644", got)
	}
	// Second deploy of the SAME path now carries the exec bit.
	if err := writeStackFiles(dir, map[string]string{"s.sh": "v2"}, map[string]uint32{"s.sh": 0755}); err != nil {
		t.Fatalf("redeploy writeStackFiles: %v", err)
	}
	if got := statPerm(t, dir, "s.sh"); got != 0755 {
		t.Errorf("after redeploy mode = %o, want 0755 (exec bit must be set on an existing file)", got)
	}
	// And the reverse: a formerly-exec file losing its bit on redeploy.
	if err := writeStackFiles(dir, map[string]string{"s.sh": "v3"}, nil); err != nil {
		t.Fatalf("third writeStackFiles: %v", err)
	}
	if got := statPerm(t, dir, "s.sh"); got != 0644 {
		t.Errorf("after bit removed mode = %o, want 0644", got)
	}
}

func TestWriteStackFilesRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	err := writeStackFiles(dir, map[string]string{"../escape": "nope"}, nil)
	if err == nil {
		t.Fatal("expected traversal rejection, got nil")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Errorf("error = %q, want a traversal rejection", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); statErr == nil {
		t.Error("traversal file was written outside stackDir")
	}
}

func statPerm(t *testing.T, dir, rel string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	return fi.Mode().Perm()
}
