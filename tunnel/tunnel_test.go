package tunnel

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name    string
		msg     Message
		wantErr bool
	}{
		{name: "register ok", msg: Message{Type: MsgRegister, Token: "abc", ProtocolVersion: ProtocolVersion, MaxConnections: 4}},
		{name: "register join ok without max connections", msg: Message{Type: MsgRegister, Token: "abc", ProtocolVersion: ProtocolVersion, SessionID: "sess-1"}},
		{name: "register missing token", msg: Message{Type: MsgRegister, ProtocolVersion: ProtocolVersion, MaxConnections: 4}, wantErr: true},
		{name: "register missing protocol version", msg: Message{Type: MsgRegister, Token: "abc", MaxConnections: 4}, wantErr: true},
		{name: "register missing max connections", msg: Message{Type: MsgRegister, Token: "abc", ProtocolVersion: ProtocolVersion}, wantErr: true},
		{name: "register invalid max connections", msg: Message{Type: MsgRegister, Token: "abc", ProtocolVersion: ProtocolVersion, MaxConnections: 0}, wantErr: true},
		{name: "register legacy version rejected", msg: Message{Type: MsgRegister, Token: "abc", ProtocolVersion: 1}, wantErr: true},
		{name: "register ack ok", msg: Message{Type: MsgRegisterAck, Subdomain: "foo", ProtocolVersion: ProtocolVersion, SessionID: "sess-1", MaxConnections: 4}},
		{name: "register ack missing subdomain", msg: Message{Type: MsgRegisterAck, ProtocolVersion: ProtocolVersion, SessionID: "sess-1", MaxConnections: 4}, wantErr: true},
		{name: "register ack missing protocol version", msg: Message{Type: MsgRegisterAck, Subdomain: "foo", SessionID: "sess-1", MaxConnections: 4}, wantErr: true},
		{name: "register ack missing session id", msg: Message{Type: MsgRegisterAck, Subdomain: "foo", ProtocolVersion: ProtocolVersion, MaxConnections: 4}, wantErr: true},
		{name: "register ack missing max connections", msg: Message{Type: MsgRegisterAck, Subdomain: "foo", ProtocolVersion: ProtocolVersion, SessionID: "sess-1"}, wantErr: true},
		{name: "register ack legacy version missing fields", msg: Message{Type: MsgRegisterAck, Subdomain: "foo", ProtocolVersion: 1}, wantErr: true},
		{name: "http request ok", msg: Message{Type: MsgHTTPRequest, ID: "req1", Method: http.MethodGet, Path: "/"}},
		{name: "http request missing id", msg: Message{Type: MsgHTTPRequest, Method: "GET", Path: "/"}, wantErr: true},
		{name: "http request missing method", msg: Message{Type: MsgHTTPRequest, ID: "r", Path: "/"}, wantErr: true},
		{name: "http request missing path", msg: Message{Type: MsgHTTPRequest, ID: "r", Method: "GET"}, wantErr: true},
		{name: "http response ok", msg: Message{Type: MsgHTTPResponse, ID: "r", Status: 200}},
		{name: "http response status 0 ok", msg: Message{Type: MsgHTTPResponse, ID: "r", Status: 0}},
		{name: "http response missing id", msg: Message{Type: MsgHTTPResponse, Status: 200}, wantErr: true},
		{name: "http response negative status", msg: Message{Type: MsgHTTPResponse, ID: "r", Status: -1}, wantErr: true},
		{name: "ping ok", msg: Message{Type: MsgPing}},
		{name: "pong ok", msg: Message{Type: MsgPong}},
		{name: "error ok", msg: Message{Type: MsgError, Error: "something"}},
		{name: "error missing body", msg: Message{Type: MsgError}, wantErr: true},
		{name: "ws open ok", msg: Message{Type: MsgWSOpen, ID: "r", Path: "/ws"}},
		{name: "ws open missing id", msg: Message{Type: MsgWSOpen, Path: "/ws"}, wantErr: true},
		{name: "ws open ack without path ok", msg: Message{Type: MsgWSOpen, ID: "r"}},
		{name: "ws frame ok", msg: Message{Type: MsgWSFrame, ID: "r"}},
		{name: "ws frame missing id", msg: Message{Type: MsgWSFrame}, wantErr: true},
		{name: "ws close ok", msg: Message{Type: MsgWSClose, ID: "r"}},
		{name: "ws close missing id", msg: Message{Type: MsgWSClose}, wantErr: true},
		{name: "key exchange ok", msg: Message{Type: MsgKeyExchange, ID: "r"}},
		{name: "key exchange missing id", msg: Message{Type: MsgKeyExchange}, wantErr: true},
		{name: "unknown type", msg: Message{Type: 100}, wantErr: true},
		{name: "zero type", msg: Message{Type: 0}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestCloneHeaders(t *testing.T) {
	t.Run("deep copy", func(t *testing.T) {
		h := http.Header{"X-Test": {"a", "b"}}
		cloned := CloneHeaders(h)
		cloned["X-Test"][0] = "changed"

		if h.Get("X-Test") != "a" {
			t.Fatalf("expected original header to stay unchanged, got %q", h.Get("X-Test"))
		}
	})

	t.Run("nil returns nil", func(t *testing.T) {
		cloned := CloneHeaders(nil)
		if cloned != nil {
			t.Fatalf("expected nil, got %v", cloned)
		}
	})

	t.Run("empty returns nil", func(t *testing.T) {
		cloned := CloneHeaders(http.Header{})
		if cloned != nil {
			t.Fatalf("expected nil for empty header, got %v", cloned)
		}
	})

	t.Run("preserves multiple values", func(t *testing.T) {
		h := http.Header{"X-Multi": {"val1", "val2", "val3"}}
		cloned := CloneHeaders(h)
		if len(cloned["X-Multi"]) != 3 {
			t.Fatalf("expected 3 values, got %d", len(cloned["X-Multi"]))
		}
	})
}

// --- WebSocket transport tests ---

// setupWSPair creates a connected pair of WebSocketConnections using httptest.
// Uses CloseNow for cleanup to avoid 5-second close handshake timeouts.
func setupWSPair(t *testing.T) (client *WebSocketConnection, srv *WebSocketConnection) {
	t.Helper()
	serverReady := make(chan *WebSocketConnection, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := AcceptWebSocket(w, r)
		if err != nil {
			t.Logf("accept error: %v", err)
			return
		}
		serverReady <- conn
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	clientConn, err := DialWebSocket(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	// Use CloseNow (via underlying conn) to avoid blocking close handshake in cleanup.
	t.Cleanup(func() { _ = clientConn.conn.CloseNow() })

	select {
	case srvConn := <-serverReady:
		t.Cleanup(func() { _ = srvConn.conn.CloseNow() })
		return clientConn, srvConn
	case <-ctx.Done():
		t.Fatal("timed out waiting for server-side connection")
		return nil, nil
	}
}

func TestWebSocketConnection_SendReceive(t *testing.T) {
	client, srv := setupWSPair(t)

	msg := Message{
		Type:   MsgHTTPRequest,
		ID:     "req-1",
		Method: "GET",
		Path:   "/test",
		Headers: map[string][]string{
			"Accept": {"application/json"},
		},
		Body: []byte("hello"),
	}

	if err := client.Send(msg); err != nil {
		t.Fatalf("client send: %v", err)
	}

	received, err := srv.Receive()
	if err != nil {
		t.Fatalf("server receive: %v", err)
	}

	if received.Type != msg.Type {
		t.Fatalf("type: got %d, want %d", received.Type, msg.Type)
	}
	if received.ID != msg.ID {
		t.Fatalf("id: got %q, want %q", received.ID, msg.ID)
	}
	if received.Method != msg.Method {
		t.Fatalf("method: got %q, want %q", received.Method, msg.Method)
	}
	if received.Path != msg.Path {
		t.Fatalf("path: got %q, want %q", received.Path, msg.Path)
	}
	if string(received.Body) != string(msg.Body) {
		t.Fatalf("body: got %q, want %q", received.Body, msg.Body)
	}
	if received.Headers["Accept"][0] != "application/json" {
		t.Fatalf("header Accept: got %q", received.Headers["Accept"])
	}
}

func TestWebSocketConnection_SendReceiveReverse(t *testing.T) {
	client, srv := setupWSPair(t)

	msg := Message{
		Type:      MsgHTTPResponse,
		ID:        "req-1",
		Status:    200,
		Body:      []byte("response body"),
		EndStream: true,
	}

	if err := srv.Send(msg); err != nil {
		t.Fatalf("server send: %v", err)
	}

	received, err := client.Receive()
	if err != nil {
		t.Fatalf("client receive: %v", err)
	}

	if received.Status != 200 {
		t.Fatalf("status: got %d, want 200", received.Status)
	}
	if !received.EndStream {
		t.Fatal("expected EndStream=true")
	}
}

func TestWebSocketConnection_SendWithTiming(t *testing.T) {
	client, srv := setupWSPair(t)

	msg := Message{
		Type:   MsgHTTPRequest,
		ID:     "req-timed",
		Method: "GET",
		Path:   "/timed",
		Body:   []byte("hello"),
	}

	timing, err := client.SendWithTiming(msg)
	if err != nil {
		t.Fatalf("client send with timing: %v", err)
	}

	if timing.WriteLockWait < 0 {
		t.Fatalf("write lock wait: got %v, want >= 0", timing.WriteLockWait)
	}
	if timing.FrameEncode < 0 {
		t.Fatalf("frame encode: got %v, want >= 0", timing.FrameEncode)
	}
	if timing.WebSocketWrite < 0 {
		t.Fatalf("websocket write: got %v, want >= 0", timing.WebSocketWrite)
	}
	if timing.Total() != timing.WriteLockWait+timing.FrameEncode+timing.WebSocketWrite {
		t.Fatalf("total timing mismatch: got %v", timing.Total())
	}

	received, err := srv.Receive()
	if err != nil {
		t.Fatalf("server receive: %v", err)
	}
	if received.ID != msg.ID {
		t.Fatalf("id: got %q, want %q", received.ID, msg.ID)
	}
}

func TestWebSocketConnection_PingPong(t *testing.T) {
	client, srv := setupWSPair(t)

	if err := client.Send(Message{Type: MsgPing}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	ping, err := srv.Receive()
	if err != nil {
		t.Fatalf("receive ping: %v", err)
	}
	if ping.Type != MsgPing {
		t.Fatalf("expected MsgPing, got %d", ping.Type)
	}

	if err := srv.Send(Message{Type: MsgPong}); err != nil {
		t.Fatalf("send pong: %v", err)
	}

	pong, err := client.Receive()
	if err != nil {
		t.Fatalf("receive pong: %v", err)
	}
	if pong.Type != MsgPong {
		t.Fatalf("expected MsgPong, got %d", pong.Type)
	}
}

func TestWebSocketConnection_RemoteAddr(t *testing.T) {
	client, srv := setupWSPair(t)

	if client.RemoteAddr() == "" {
		t.Fatal("client RemoteAddr should not be empty")
	}
	if srv.RemoteAddr() == "" {
		t.Fatal("server RemoteAddr should not be empty")
	}
}

func TestWebSocketConnection_CloseAndReceiveError(t *testing.T) {
	client, srv := setupWSPair(t)

	// Close the client side (may error due to close handshake timeout, that's ok)
	_ = client.Close()

	// Server should get an error on next Receive
	_, err := srv.Receive()
	if err == nil {
		t.Fatal("expected error after peer close, got nil")
	}
}

func TestWebSocketTransport_ListenAcceptClose(t *testing.T) {
	transport := NewWebSocketTransport()

	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Dial into the listener
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsLn := ln.(*websocketListener)
	addr := wsLn.ln.Addr().String()

	conn, err := transport.Dial(ctx, "ws://"+addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Accept the connection
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Round-trip a message
	if err := conn.Send(Message{Type: MsgPing}); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := accepted.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Type != MsgPing {
		t.Fatalf("expected MsgPing, got %d", msg.Type)
	}

	// Clean up connections before closing listener to avoid races
	_ = conn.Close()
	_ = accepted.Close()

	// Close listener (Shutdown may already close the net.Listener, so ignore errors)
	_ = ln.Close()
}

func TestWebSocketTransport_ListenAcceptAfterClose(t *testing.T) {
	transport := NewWebSocketTransport()

	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = ln.Accept()
	if err == nil {
		t.Fatal("expected error from Accept after Close")
	}
}

func TestWebSocketTransport_DialUnreachable(t *testing.T) {
	transport := NewWebSocketTransport()

	// Pick a port that's not listening
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = transport.Dial(ctx, "ws://"+addr)
	if err == nil {
		t.Fatal("expected error dialing unreachable address")
	}
}

func TestDialWebSocket_WithHeaders(t *testing.T) {
	var receivedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		conn, err := AcceptWebSocket(w, r)
		if err != nil {
			return
		}
		defer func() { _ = conn.conn.CloseNow() }()
		_, _ = conn.Receive()
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	headers := http.Header{
		"X-Custom-Header": {"custom-value"},
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, err := DialWebSocket(ctx, wsURL, headers)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.conn.CloseNow() }()

	if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Fatalf("custom header not received, got headers: %v", receivedHeaders)
	}
}

func TestDialWebSocket_RemoteAddrIsParsed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := AcceptWebSocket(w, r)
		if err != nil {
			return
		}
		defer func() { _ = conn.conn.CloseNow() }()
		_, _ = conn.Receive()
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, err := DialWebSocket(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.conn.CloseNow() }()

	addr := conn.RemoteAddr()
	if addr == "" {
		t.Fatal("RemoteAddr should not be empty")
	}
	if !strings.Contains(addr, "127.0.0.1") {
		t.Fatalf("RemoteAddr should contain 127.0.0.1, got %q", addr)
	}
}

func TestNewWebSocketTransport_DefaultTimeouts(t *testing.T) {
	transport := NewWebSocketTransport()
	if transport.ReadTimeout != defaultReadTimeout {
		t.Fatalf("ReadTimeout: got %v, want %v", transport.ReadTimeout, defaultReadTimeout)
	}
	if transport.WriteTimeout != defaultWriteTimeout {
		t.Fatalf("WriteTimeout: got %v, want %v", transport.WriteTimeout, defaultWriteTimeout)
	}
}

func TestWebSocketTransport_TimeoutHelpers(t *testing.T) {
	t.Run("custom timeouts", func(t *testing.T) {
		transport := &WebSocketTransport{
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 15 * time.Second,
		}
		if transport.readTimeout() != 30*time.Second {
			t.Fatalf("readTimeout: got %v", transport.readTimeout())
		}
		if transport.writeTimeout() != 15*time.Second {
			t.Fatalf("writeTimeout: got %v", transport.writeTimeout())
		}
	})

	t.Run("zero uses defaults", func(t *testing.T) {
		transport := &WebSocketTransport{}
		if transport.readTimeout() != defaultReadTimeout {
			t.Fatalf("readTimeout: got %v, want %v", transport.readTimeout(), defaultReadTimeout)
		}
		if transport.writeTimeout() != defaultWriteTimeout {
			t.Fatalf("writeTimeout: got %v, want %v", transport.writeTimeout(), defaultWriteTimeout)
		}
	})

	t.Run("negative uses defaults", func(t *testing.T) {
		transport := &WebSocketTransport{
			ReadTimeout:  -1,
			WriteTimeout: -1,
		}
		if transport.readTimeout() != defaultReadTimeout {
			t.Fatalf("readTimeout: got %v, want %v", transport.readTimeout(), defaultReadTimeout)
		}
		if transport.writeTimeout() != defaultWriteTimeout {
			t.Fatalf("writeTimeout: got %v, want %v", transport.writeTimeout(), defaultWriteTimeout)
		}
	})
}

func TestWebSocketConnection_MultipleMessages(t *testing.T) {
	client, srv := setupWSPair(t)

	for i := 0; i < 10; i++ {
		msg := Message{
			Type:   MsgHTTPRequest,
			ID:     "req-" + strings.Repeat("x", i+1),
			Method: "GET",
			Path:   "/",
		}
		if err := client.Send(msg); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	for i := 0; i < 10; i++ {
		received, err := srv.Receive()
		if err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
		expectedID := "req-" + strings.Repeat("x", i+1)
		if received.ID != expectedID {
			t.Fatalf("message %d: got ID %q, want %q", i, received.ID, expectedID)
		}
	}
}

func TestWebSocketConnection_LargeBody(t *testing.T) {
	client, srv := setupWSPair(t)

	largeBody := make([]byte, 1*1024*1024)
	for i := range largeBody {
		largeBody[i] = byte(i % 256)
	}

	msg := Message{
		Type:      MsgHTTPResponse,
		ID:        "large",
		Status:    200,
		Body:      largeBody,
		EndStream: true,
	}

	if err := client.Send(msg); err != nil {
		t.Fatalf("send large message: %v", err)
	}

	received, err := srv.Receive()
	if err != nil {
		t.Fatalf("receive large message: %v", err)
	}

	if len(received.Body) != len(largeBody) {
		t.Fatalf("body length: got %d, want %d", len(received.Body), len(largeBody))
	}
}

func TestWebSocketListener_DoubleClose(t *testing.T) {
	transport := NewWebSocketTransport()
	ln, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// First close
	_ = ln.Close()

	// Second close should be idempotent (sync.Once)
	if err := ln.Close(); err != nil {
		t.Fatalf("second close should be nil, got: %v", err)
	}
}

func TestWebSocketConnection_WSBinaryFlag(t *testing.T) {
	client, srv := setupWSPair(t)

	msg := Message{
		Type:     MsgWSFrame,
		ID:       "ws-1",
		Body:     []byte{0x00, 0xFF, 0x80},
		WSBinary: true,
	}

	if err := client.Send(msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	received, err := srv.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if !received.WSBinary {
		t.Fatal("expected WSBinary=true")
	}
	if len(received.Body) != 3 {
		t.Fatalf("body length: got %d, want 3", len(received.Body))
	}
}

func TestWebSocketConnection_ReceiveRejectsTextFrames(t *testing.T) {
	client, srv := setupWSPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.conn.Write(ctx, websocket.MessageText, []byte(`{"type":5}`)); err != nil {
		t.Fatalf("write text frame: %v", err)
	}

	_, err := srv.Receive()
	if err == nil {
		t.Fatal("expected receive error for text frame")
	}
	if !strings.Contains(err.Error(), "expected binary frame") {
		t.Fatalf("expected binary-frame error, got %v", err)
	}
}
