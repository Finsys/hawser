package edge

import (
	"testing"
	"time"

	"github.com/Finsys/hawser/internal/config"
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
