package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Finsys/hawser/internal/config"
	"github.com/Finsys/hawser/internal/docker"
	"github.com/Finsys/hawser/internal/protocol"
	"github.com/gorilla/websocket"
)

// fakeComposeClient is a test double for composeExecutor. Execute never
// shells out to a real docker/docker-compose binary -- it just calls onLine
// with the canned lines and returns the canned result. That keeps these
// tests independent of whether Docker is installed, unlike the one
// Docker-dependent test in internal/docker (gated behind HAWSER_TEST_DOCKER).
type fakeComposeClient struct {
	lines  []string
	result *docker.ComposeResult
}

func (f *fakeComposeClient) Execute(_ context.Context, _ *docker.ComposeOperation, onLine func(string)) (*docker.ComposeResult, error) {
	if onLine != nil {
		for _, l := range f.lines {
			onLine(l)
		}
	}
	if f.result != nil {
		return f.result, nil
	}
	return &docker.ComposeResult{Success: true}, nil
}

func (f *fakeComposeClient) IsAvailable() bool { return true }

// sentMessage is the minimal shape needed to classify a captured message by
// its "type" field, without depending on every concrete protocol.*Message
// type.
type sentMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
}

// captureSentMessages spins up a real (loopback) websocket server, connects
// a *Client to it exactly like production code does, and returns that
// client plus a drain function. sendJSON writes through a real
// *websocket.Conn (gorilla, a concrete type -- there's no interface to fake
// here without changing production code well beyond this task's scope), so
// a real connection is the only way to observe what it sends.
//
// drain(wantType) blocks until a message of type wantType has been
// received (handleComposeRequest always ends with exactly one "response" or
// "error" message, so tests wait for that) or a timeout elapses, then
// returns every message captured so far.
func captureSentMessages(t *testing.T) (client *Client, drain func(wantType string) []sentMessage) {
	t.Helper()

	var (
		mu  sync.Mutex
		got []sentMessage
	)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m sentMessage
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
		}
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial test websocket server: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })

	client = &Client{conn: clientConn}

	drain = func(wantType string) []sentMessage {
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			for _, m := range got {
				if m.Type == wantType {
					out := make([]sentMessage, len(got))
					copy(out, got)
					mu.Unlock()
					return out
				}
			}
			mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for a %q message", wantType)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	return client, drain
}

func countByType(msgs []sentMessage, typ string) int {
	n := 0
	for _, m := range msgs {
		if m.Type == typ {
			n++
		}
	}
	return n
}

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	return data
}

func TestComposeRequestSendsLinesWhenAsked(t *testing.T) {
	c, drain := captureSentMessages(t)
	c.compose = &fakeComposeClient{lines: []string{"pulling image", "container started"}}

	req := &protocol.RequestMessage{
		RequestID: "req-1",
		Path:      "/_hawser/compose",
		Body:      mustJSON(t, docker.ComposeOperation{Operation: "up", StreamOutput: true}),
	}
	c.handleComposeRequest(context.Background(), req)

	sent := drain("response")
	if got := countByType(sent, "stream"); got == 0 {
		t.Fatal("no line message sent, although StreamOutput was true")
	}
	if got := countByType(sent, "response"); got != 1 {
		t.Fatalf("final response missing or sent more than once: got %d", got)
	}
}

func TestComposeRequestStaysSilentByDefault(t *testing.T) {
	c, drain := captureSentMessages(t)
	c.compose = &fakeComposeClient{lines: []string{"pulling image", "container started"}}

	req := &protocol.RequestMessage{
		RequestID: "req-2",
		Path:      "/_hawser/compose",
		Body:      mustJSON(t, docker.ComposeOperation{Operation: "up"}), // no StreamOutput
	}
	c.handleComposeRequest(context.Background(), req)

	sent := drain("response")
	if got := countByType(sent, "stream"); got != 0 {
		t.Fatalf("lines sent although not requested (got %d) -- breaks older Dockhand versions", got)
	}
	if got := countByType(sent, "response"); got != 1 {
		t.Fatalf("final response missing or sent more than once: got %d", got)
	}
}

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
