package edge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
)

// TestRequestTimeout_ComposeGetsOwnBudget verifies that compose requests use
// ComposeTimeout, and every other path (including a path that merely
// contains "compose" as a substring) falls back to RequestTimeout. Before
// this fix, /_hawser/compose inherited RequestTimeout, so a long-running
// deploy (image pull + container recreation + depends_on health-check wait)
// was aborted at 30s even though it was still making progress.
func TestRequestTimeout_ComposeGetsOwnBudget(t *testing.T) {
	cfg := &config.Config{RequestTimeout: 30, ComposeTimeout: 900}
	c := &Client{cfg: cfg}

	tests := []struct {
		name string
		path string
		want time.Duration
	}{
		{"compose path gets ComposeTimeout", "/_hawser/compose", 900 * time.Second},
		{"regular docker path gets RequestTimeout", "/containers/json", 30 * time.Second},
		{"path containing compose as substring is not special-cased", "/_hawser/compose/logs", 30 * time.Second},
		{"root path gets RequestTimeout", "/", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.requestTimeout(tt.path); got != tt.want {
				t.Errorf("requestTimeout(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestRequestTimeout_ComposeLongerThanRequest is the regression guard: it
// fails if ComposeTimeout is ever wired back to RequestTimeout (the original
// bug), regardless of what default values either field happens to have.
func TestRequestTimeout_ComposeLongerThanRequest(t *testing.T) {
	cfg := &config.Config{RequestTimeout: 30, ComposeTimeout: 900}
	c := &Client{cfg: cfg}

	composeBudget := c.requestTimeout("/_hawser/compose")
	requestBudget := c.requestTimeout("/containers/json")

	if composeBudget <= requestBudget {
		t.Fatalf("compose timeout (%v) must be greater than the plain request timeout (%v)", composeBudget, requestBudget)
	}
}

// pastDeadlineContext returns a context whose deadline is already behind us,
// so ctx.Err() is context.DeadlineExceeded immediately — no need to sleep out
// a real timeout to exercise the "context already expired" branch.
func pastDeadlineContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel() // avoid a leak lint warning; the deadline already fired synchronously
	return ctx
}

// TestAnnotateComposeTimeout_ExpiredContextOnFailure is the case this fix
// exists for: the docker-compose subprocess got killed because our own
// context timeout expired, and cmd.Run() surfaces that as a generic error
// (e.g. "signal: killed") indistinguishable from a real compose failure. The
// annotation must name the setting (COMPOSE_TIMEOUT), state the configured
// duration, and keep the original error text rather than discard it.
func TestAnnotateComposeTimeout_ExpiredContextOnFailure(t *testing.T) {
	c := &Client{cfg: &config.Config{ComposeTimeout: 900}}
	result := &docker.ComposeResult{
		Success: false,
		Output:  "Pulling web ... \nCreating app_web_1 ... \n",
		Error:   "signal: killed",
	}

	c.annotateComposeTimeout(pastDeadlineContext(), result)

	if !strings.Contains(result.Error, "COMPOSE_TIMEOUT") {
		t.Errorf("annotated error = %q, want it to name COMPOSE_TIMEOUT", result.Error)
	}
	if !strings.Contains(result.Error, "900") {
		t.Errorf("annotated error = %q, want it to state the configured duration (900)", result.Error)
	}
	if !strings.Contains(result.Error, "signal: killed") {
		t.Errorf("annotated error = %q, want the original error preserved, not discarded", result.Error)
	}
	if !strings.Contains(result.Output, "Creating app_web_1") {
		t.Errorf("Output = %q, must not be touched by the annotation", result.Output)
	}
}

// TestAnnotateComposeTimeout_LeavesGenuineFailureAlone verifies a real
// compose failure (context still live, e.g. a missing image) is untouched —
// only a context-timeout-caused failure gets the extra explanation.
func TestAnnotateComposeTimeout_LeavesGenuineFailureAlone(t *testing.T) {
	c := &Client{cfg: &config.Config{ComposeTimeout: 900}}
	result := &docker.ComposeResult{
		Success: false,
		Error:   "pull access denied for ghcr.io/example/missing, repository does not exist",
	}
	want := result.Error

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour) // nowhere near expiring
	defer cancel()
	c.annotateComposeTimeout(ctx, result)

	if result.Error != want {
		t.Errorf("annotateComposeTimeout changed a genuine failure's error: got %q, want unchanged %q", result.Error, want)
	}
}

// TestAnnotateComposeTimeout_LeavesSuccessAlone: an expired context racing
// against a subprocess that happened to finish successfully must not have
// its (empty) error field rewritten into a spurious timeout notice.
func TestAnnotateComposeTimeout_LeavesSuccessAlone(t *testing.T) {
	c := &Client{cfg: &config.Config{ComposeTimeout: 900}}
	result := &docker.ComposeResult{Success: true, Output: "done"}

	c.annotateComposeTimeout(pastDeadlineContext(), result)

	if result.Error != "" {
		t.Errorf("annotateComposeTimeout touched a successful result: Error = %q, want empty", result.Error)
	}
}

// TestAnnotateComposeTimeout_PlainCancelIsNotDeadlineExceeded: an explicitly
// canceled context (context.Canceled) is a different signal than a timeout
// (context.DeadlineExceeded) and must not trigger the timeout annotation.
func TestAnnotateComposeTimeout_PlainCancelIsNotDeadlineExceeded(t *testing.T) {
	c := &Client{cfg: &config.Config{ComposeTimeout: 900}}
	result := &docker.ComposeResult{Success: false, Error: "signal: killed"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.annotateComposeTimeout(ctx, result)

	if result.Error != "signal: killed" {
		t.Errorf("annotateComposeTimeout fired on plain cancellation: Error = %q, want unchanged", result.Error)
	}
}
