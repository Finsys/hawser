package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Protocol version
const ProtocolVersion = "1.0"

// Message types
const (
	TypeHello          = "hello"           // Agent → Dockhand: Initial connection
	TypeWelcome        = "welcome"         // Dockhand → Agent: Connection accepted
	TypeRequest        = "request"         // Dockhand → Agent: Docker API request
	TypeResponse       = "response"        // Agent → Dockhand: Docker API response
	TypeStream         = "stream"          // Bidirectional: Streaming data (logs, exec)
	TypeStreamEnd      = "stream_end"      // End of stream
	TypeMetrics        = "metrics"         // Agent → Dockhand: Host metrics
	TypeContainerEvent = "container_event" // Agent → Dockhand: Docker container event
	TypePing           = "ping"            // Keepalive request
	TypePong           = "pong"            // Keepalive response
	TypeError          = "error"           // Error message

	// Exec-specific message types for bidirectional terminal
	TypeExecStart  = "exec_start"  // Dockhand → Agent: Start exec session
	TypeExecReady  = "exec_ready"  // Agent → Dockhand: Exec session ready
	TypeExecInput  = "exec_input"  // Dockhand → Agent: Terminal input from user
	TypeExecOutput = "exec_output" // Agent → Dockhand: Terminal output to user
	TypeExecResize = "exec_resize" // Dockhand → Agent: Terminal resize
	TypeExecEnd    = "exec_end"    // Bidirectional: End exec session
)

// Agent capabilities
const (
	CapabilityCompose = "compose" // Docker Compose support
	CapabilityExec    = "exec"    // Interactive exec support
	CapabilityMetrics = "metrics" // Host metrics collection
	CapabilityEvents  = "events"  // Docker event streaming
)

// BaseMessage is the common structure for all messages
type BaseMessage struct {
	Type string `json:"type"`
}

// HelloMessage is sent by agent on connect
type HelloMessage struct {
	Type          string   `json:"type"`
	Version       string   `json:"version"`         // Hawser agent version
	Protocol      string   `json:"protocol"`        // Protocol version for compatibility
	AgentID       string   `json:"agentId"`
	AgentName     string   `json:"agentName"`
	Token         string   `json:"token"`
	DockerVersion string   `json:"dockerVersion"`
	Hostname      string   `json:"hostname"`
	Capabilities  []string `json:"capabilities"`
}

// NewHelloMessage creates a new hello message
func NewHelloMessage(agentID, agentName, token, dockerVersion, hostname, hawserVersion string, capabilities []string) *HelloMessage {
	return &HelloMessage{
		Type:          TypeHello,
		Version:       hawserVersion,
		Protocol:      ProtocolVersion,
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
	Body       string            `json:"body,omitempty"`       // Base64-encoded for binary, plain string for JSON
	IsBinary   bool              `json:"isBinary,omitempty"`   // True if Body is base64-encoded binary data
}

// NewResponseMessage creates a new response message
// For JSON responses, body is sent as-is
// For binary responses (logs, tar, etc.), body is base64-encoded and IsBinary is set to true
func NewResponseMessage(requestID string, statusCode int, headers map[string]string, body []byte) *ResponseMessage {
	// Check if this is binary data (not valid JSON or contains non-printable chars)
	isBinary := false
	bodyStr := ""

	if len(body) > 0 {
		// Check for common binary indicators
		contentType := ""
		for k, v := range headers {
			if k == "Content-Type" || k == "content-type" {
				contentType = v
				break
			}
		}

		// Binary content types
		if strings.Contains(contentType, "octet-stream") ||
			strings.Contains(contentType, "raw-stream") ||
			strings.Contains(contentType, "tar") ||
			strings.Contains(contentType, "gzip") {
			isBinary = true
		}

		// Check for non-printable bytes (binary data)
		if !isBinary {
			for _, b := range body {
				// Allow printable ASCII, tabs, newlines, carriage returns
				if b < 0x09 || (b > 0x0D && b < 0x20) || b == 0x7F {
					isBinary = true
					break
				}
			}
		}

		if isBinary {
			bodyStr = base64.StdEncoding.EncodeToString(body)
		} else {
			bodyStr = string(body)
		}
	}

	return &ResponseMessage{
		Type:       TypeResponse,
		RequestID:  requestID,
		StatusCode: statusCode,
		Headers:    headers,
		Body:       bodyStr,
		IsBinary:   isBinary,
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
	Uptime         uint64  `json:"uptime"`         // Host uptime in seconds
}

// NewMetricsMessage creates a new metrics message
func NewMetricsMessage(timestamp int64, metrics HostMetrics) *MetricsMessage {
	return &MetricsMessage{
		Type:      TypeMetrics,
		Timestamp: timestamp,
		Metrics:   metrics,
	}
}

// ContainerEventMessage contains a Docker container event
type ContainerEventMessage struct {
	Type  string         `json:"type"`
	Event ContainerEvent `json:"event"`
}

// ContainerEvent contains container event details
type ContainerEvent struct {
	ContainerID     string            `json:"containerId"`
	ContainerName   string            `json:"containerName,omitempty"`
	Image           string            `json:"image,omitempty"`
	Action          string            `json:"action"`
	ActorAttributes map[string]string `json:"actorAttributes,omitempty"`
	Timestamp       string            `json:"timestamp"` // ISO 8601 format
}

// NewContainerEventMessage creates a new container event message
func NewContainerEventMessage(event ContainerEvent) *ContainerEventMessage {
	return &ContainerEventMessage{
		Type:  TypeContainerEvent,
		Event: event,
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

// ExecStartMessage requests starting an exec session
type ExecStartMessage struct {
	Type        string `json:"type"`
	ExecID      string `json:"execId"`      // Unique ID for this exec session
	ContainerID string `json:"containerId"` // Container to exec into
	Cmd         string `json:"cmd"`         // Command to run (e.g., "/bin/sh")
	User        string `json:"user"`        // User to run as
	Cols        int    `json:"cols"`        // Initial terminal columns
	Rows        int    `json:"rows"`        // Initial terminal rows
}

// ExecReadyMessage confirms exec session is ready
type ExecReadyMessage struct {
	Type   string `json:"type"`
	ExecID string `json:"execId"`
}

// NewExecReadyMessage creates a new exec ready message
func NewExecReadyMessage(execID string) *ExecReadyMessage {
	return &ExecReadyMessage{
		Type:   TypeExecReady,
		ExecID: execID,
	}
}

// ExecInputMessage sends terminal input to the exec session
type ExecInputMessage struct {
	Type   string `json:"type"`
	ExecID string `json:"execId"`
	Data   string `json:"data"` // Base64-encoded input data
}

// ExecOutputMessage sends terminal output from the exec session
type ExecOutputMessage struct {
	Type   string `json:"type"`
	ExecID string `json:"execId"`
	Data   string `json:"data"` // Base64-encoded output data
}

// NewExecOutputMessage creates a new exec output message
func NewExecOutputMessage(execID string, data []byte) *ExecOutputMessage {
	return &ExecOutputMessage{
		Type:   TypeExecOutput,
		ExecID: execID,
		Data:   base64.StdEncoding.EncodeToString(data),
	}
}

// ExecResizeMessage resizes the exec terminal
type ExecResizeMessage struct {
	Type   string `json:"type"`
	ExecID string `json:"execId"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// ExecEndMessage ends an exec session
type ExecEndMessage struct {
	Type   string `json:"type"`
	ExecID string `json:"execId"`
	Reason string `json:"reason,omitempty"` // "user_closed", "container_exit", "error"
}

// NewExecEndMessage creates a new exec end message
func NewExecEndMessage(execID string, reason string) *ExecEndMessage {
	return &ExecEndMessage{
		Type:   TypeExecEnd,
		ExecID: execID,
		Reason: reason,
	}
}
