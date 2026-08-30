package docker

import (
	"context"
	"strings"
	"testing"
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
