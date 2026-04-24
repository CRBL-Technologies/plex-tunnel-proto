package tunnel

import (
	"errors"
	"fmt"
	"net/http"
)

// MessageType identifies the kind of tunnel protocol message.
type MessageType uint8

const (
	MsgRegister MessageType = iota + 1
	MsgRegisterAck
	MsgHTTPRequest
	MsgHTTPResponse
	MsgPing
	MsgPong
	MsgError
	MsgWSOpen               // server asks client to open a WebSocket to local upstream
	MsgWSFrame              // a WebSocket frame forwarded in either direction
	MsgWSClose              // signals that the WebSocket session should be torn down
	MsgKeyExchange          // reserved for future end-to-end encryption key exchange
	MsgMaxConnectionsUpdate // server tells client to adjust its connection pool size
	MsgCancel               // server tells client to abort an in-flight request (e.g. downstream disconnect)
	MsgWSWindowUpdate
)

// Capability bits are advertised on MsgRegister/MsgRegisterAck and govern
// optional protocol behaviour for the lifetime of the session.
const (
	// CapLeasedPool indicates the peer supports the leased-tunnel-pool
	// data plane (1 control WS + N data WS, leased-exclusive data lanes).
	// Both peers MUST advertise this bit for the server to enable
	// leased-pool routing for the session.
	CapLeasedPool    uint32 = 1 << 0
	CapWSFlowControl uint32 = 1 << 1
)

// ProtocolVersion is the current protocol version. Peers must negotiate
// this version (or higher) during the Register/RegisterAck handshake.
const ProtocolVersion uint16 = 3

// Message is the application-level tunnel protocol unit. It is serialized
// into a Frame for transport over WebSocket.
type Message struct {
	Type      MessageType `json:"type"`
	ID        string      `json:"id,omitempty"`
	Token     string      `json:"token,omitempty"`
	Subdomain string      `json:"subdomain,omitempty"`
	// ProtocolVersion is negotiated during register/register-ack handshake.
	ProtocolVersion uint16 `json:"protocol_version,omitempty"`
	// SessionID identifies the logical tunnel session in protocol v2.
	SessionID string `json:"session_id,omitempty"`
	// MaxConnections is the requested/granted parallel connection count in protocol v2.
	MaxConnections int `json:"max_connections,omitempty"`
	// Capabilities is a bitmask of optional protocol features negotiated during
	// register/register-ack. Peers MUST treat unknown bits as reserved-zero.
	Capabilities uint32              `json:"capabilities,omitempty"`
	Method       string              `json:"method,omitempty"`
	Path         string              `json:"path,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         []byte              `json:"-"`
	Status       int                 `json:"status,omitempty"`
	// EndStream marks the final frame for a request/response stream identified
	// by ID. For streamed HTTP requests, method/path/headers are sent on the
	// first frame only; continuation frames carry body chunks plus EndStream.
	EndStream       bool   `json:"end_stream,omitempty"`
	Error           string `json:"error,omitempty"`
	WSBinary        bool   `json:"ws_binary,omitempty"` // true = binary WebSocket frame
	WindowIncrement uint32 `json:"window_increment,omitempty"`
	Encrypted       bool   `json:"encrypted,omitempty"` // reserved for future end-to-end payload encryption
}

// Validate checks that the message has all required fields for its type.
func (m Message) Validate() error {
	switch m.Type {
	case MsgRegister:
		if m.Token == "" {
			return errors.New("register message missing token")
		}
		if m.ProtocolVersion == 0 {
			return errors.New("register message missing protocol_version")
		}
		if m.ProtocolVersion < ProtocolVersion {
			return fmt.Errorf("unsupported protocol version %d (minimum %d)", m.ProtocolVersion, ProtocolVersion)
		}
	case MsgRegisterAck:
		if m.Subdomain == "" {
			return errors.New("register ack missing subdomain")
		}
		if m.ProtocolVersion == 0 {
			return errors.New("register ack missing protocol_version")
		}
		if m.ProtocolVersion < ProtocolVersion {
			return fmt.Errorf("unsupported protocol version %d (minimum %d)", m.ProtocolVersion, ProtocolVersion)
		}
		if m.SessionID == "" {
			return errors.New("register ack missing session_id")
		}
		if m.MaxConnections < 1 {
			return errors.New("register ack missing or invalid max_connections")
		}
	case MsgHTTPRequest:
		if m.ID == "" {
			return errors.New("http request message missing id")
		}
		if m.Method == "" {
			return errors.New("http request message missing method")
		}
		if m.Path == "" {
			return errors.New("http request message missing path")
		}
	case MsgHTTPResponse:
		if m.ID == "" {
			return errors.New("http response message missing id")
		}
		if m.Status < 0 {
			return fmt.Errorf("invalid http response status: %d", m.Status)
		}
	case MsgPing, MsgPong:
		return nil
	case MsgError:
		if m.Error == "" {
			return errors.New("error message missing body")
		}
	case MsgWSOpen:
		if m.ID == "" {
			return errors.New("ws open message missing id")
		}
		// Path is required for server->client open requests, but optional on
		// client->server open acknowledgements that only carry ID.
	case MsgWSFrame:
		if m.ID == "" {
			return errors.New("ws frame message missing id")
		}
	case MsgWSClose:
		if m.ID == "" {
			return errors.New("ws close message missing id")
		}
	case MsgKeyExchange:
		if m.ID == "" {
			return errors.New("key exchange message missing id")
		}
	case MsgMaxConnectionsUpdate:
		if m.MaxConnections < 1 {
			return errors.New("max connections update missing or invalid max_connections")
		}
	case MsgCancel:
		if m.ID == "" {
			return errors.New("cancel message missing id")
		}
	case MsgWSWindowUpdate:
		if m.ID == "" {
			return errors.New("ws window update message missing id")
		}
	default:
		return fmt.Errorf("unknown message type: %d", m.Type)
	}

	return nil
}

// CloneHeaders returns a deep copy of the given HTTP headers as a plain map.
func CloneHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}

	out := make(map[string][]string, len(headers))
	for k, v := range headers {
		vals := make([]string, len(v))
		copy(vals, v)
		out[k] = vals
	}
	return out
}
