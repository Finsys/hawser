package protocol

import "encoding/json"

// Protocol version
const ProtocolVersion = "1.0"

// Message types
const (
	TypeHello     = "hello"      // Agent → Dockhand: Initial connection
	TypeWelcome   = "welcome"    // Dockhand → Agent: Connection accepted
	TypeRequest   = "request"    // Dockhand → Agent: Docker API request
	TypeResponse  = "response"   // Agent → Dockhand: Docker API response
	TypeStream    = "stream"     // Bidirectional: Streaming data (logs, exec)
	TypeStreamEnd = "stream_end" // End of stream
	TypeMetrics   = "metrics"    // Agent → Dockhand: Host metrics
	TypePing      = "ping"       // Keepalive request
	TypePong      = "pong"       // Keepalive response
	TypeError     = "error"      // Error message
)

// Agent capabilities
const (
	CapabilityCompose = "compose" // Docker Compose support
	CapabilityExec    = "exec"    // Interactive exec support
	CapabilityMetrics = "metrics" // Host metrics collection
)

// BaseMessage is the common structure for all messages
type BaseMessage struct {
	Type string `json:"type"`
}

// HelloMessage is sent by agent on connect
type HelloMessage struct {
	Type          string   `json:"type"`
	Version       string   `json:"version"`
	AgentID       string   `json:"agentId"`
	AgentName     string   `json:"agentName"`
	Token         string   `json:"token"`
	DockerVersion string   `json:"dockerVersion"`
	Hostname      string   `json:"hostname"`
	Capabilities  []string `json:"capabilities"`
}

// NewHelloMessage creates a new hello message
func NewHelloMessage(agentID, agentName, token, dockerVersion, hostname string, capabilities []string) *HelloMessage {
	return &HelloMessage{
		Type:          TypeHello,
		Version:       ProtocolVersion,
		AgentID:       agentID,
		AgentName:     agentName,
		Token:         token,
		DockerVersion: dockerVersion,
		Hostname:      hostname,
		Capabilities:  capabilities,
	}
}

// WelcomeMessage is sent by Dockhand on successful auth
type WelcomeMessage struct {
	Type          string `json:"type"`
	EnvironmentID int    `json:"environmentId"`
	Message       string `json:"message,omitempty"`
}

// RequestMessage is a Docker API request from Dockhand
type RequestMessage struct {
	Type      string            `json:"type"`
	RequestID string            `json:"requestId"` // UUID for matching response
	Method    string            `json:"method"`    // HTTP method
	Path      string            `json:"path"`      // Docker API path
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
	Streaming bool              `json:"streaming"` // true for logs, exec, etc.
}

// ResponseMessage is a Docker API response to Dockhand
type ResponseMessage struct {
	Type       string            `json:"type"`
	RequestID  string            `json:"requestId"`
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
}

// NewResponseMessage creates a new response message
func NewResponseMessage(requestID string, statusCode int, headers map[string]string, body json.RawMessage) *ResponseMessage {
	return &ResponseMessage{
		Type:       TypeResponse,
		RequestID:  requestID,
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
	}
}

// StreamMessage is for streaming responses (logs, exec, events)
type StreamMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Data      []byte `json:"data"`
	Stream    string `json:"stream,omitempty"` // "stdout", "stderr", or empty
}

// NewStreamMessage creates a new stream message
func NewStreamMessage(requestID string, data []byte, stream string) *StreamMessage {
	return &StreamMessage{
		Type:      TypeStream,
		RequestID: requestID,
		Data:      data,
		Stream:    stream,
	}
}

// StreamEndMessage marks end of stream
type StreamEndMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}

// NewStreamEndMessage creates a new stream end message
func NewStreamEndMessage(requestID string, reason string) *StreamEndMessage {
	return &StreamEndMessage{
		Type:      TypeStreamEnd,
		RequestID: requestID,
		Reason:    reason,
	}
}

// MetricsMessage contains host metrics
type MetricsMessage struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Metrics   HostMetrics `json:"metrics"`
}

// HostMetrics contains CPU, memory, and disk statistics
type HostMetrics struct {
	CPUUsage       float64 `json:"cpuUsage"`       // Percentage (0-100)
	CPUCores       int     `json:"cpuCores"`       // Number of cores
	MemoryTotal    uint64  `json:"memoryTotal"`    // Bytes
	MemoryUsed     uint64  `json:"memoryUsed"`     // Bytes
	MemoryFree     uint64  `json:"memoryFree"`     // Bytes
	DiskTotal      uint64  `json:"diskTotal"`      // Bytes (Docker data-root)
	DiskUsed       uint64  `json:"diskUsed"`       // Bytes
	DiskFree       uint64  `json:"diskFree"`       // Bytes
	NetworkRxBytes uint64  `json:"networkRxBytes"` // Total received bytes
	NetworkTxBytes uint64  `json:"networkTxBytes"` // Total transmitted bytes
}

// NewMetricsMessage creates a new metrics message
func NewMetricsMessage(timestamp int64, metrics HostMetrics) *MetricsMessage {
	return &MetricsMessage{
		Type:      TypeMetrics,
		Timestamp: timestamp,
		Metrics:   metrics,
	}
}

// PingMessage is a keepalive request
type PingMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

// NewPingMessage creates a new ping message
func NewPingMessage(timestamp int64) *PingMessage {
	return &PingMessage{
		Type:      TypePing,
		Timestamp: timestamp,
	}
}

// PongMessage is a keepalive response
type PongMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

// NewPongMessage creates a new pong message
func NewPongMessage(timestamp int64) *PongMessage {
	return &PongMessage{
		Type:      TypePong,
		Timestamp: timestamp,
	}
}

// ErrorMessage is an error response
type ErrorMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId,omitempty"`
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
}

// NewErrorMessage creates a new error message
func NewErrorMessage(requestID, errorMsg, code string) *ErrorMessage {
	return &ErrorMessage{
		Type:      TypeError,
		RequestID: requestID,
		Error:     errorMsg,
		Code:      code,
	}
}

// ParseMessageType extracts the message type from raw JSON
func ParseMessageType(data []byte) (string, error) {
	var base BaseMessage
	if err := json.Unmarshal(data, &base); err != nil {
		return "", err
	}
	return base.Type, nil
}
