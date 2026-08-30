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
