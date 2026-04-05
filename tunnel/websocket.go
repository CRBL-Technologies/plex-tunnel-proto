package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

const (
	// defaultReadTimeout bounds the time for a single WebSocket message read.
	// This covers the full message including body, so it must accommodate
	// large payloads (~8 MB) on moderate connections. The server's per-request
	// timeout provides an outer bound; this timeout catches stuck reads.
	defaultReadTimeout  = 30 * time.Second
	defaultWriteTimeout = 30 * time.Second
	defaultReadLimit    = int64(8 * 1024 * 1024)
)

type WebSocketTransport struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func NewWebSocketTransport() *WebSocketTransport {
	return &WebSocketTransport{
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
	}
}

func (t *WebSocketTransport) Dial(ctx context.Context, rawURL string) (Connection, error) {
	return dialWebSocket(ctx, rawURL, nil, t.readTimeout(), t.writeTimeout())
}

func (t *WebSocketTransport) Listen(addr string) (Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen websocket transport: %w", err)
	}

	l := &websocketListener{
		ln:    ln,
		conns: make(chan Connection, 256),
		errCh: make(chan error, 2),
		done:  make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()

		conn, err := acceptWebSocket(w, r, t.readTimeout(), t.writeTimeout())
		if err != nil {
			return
		}

		select {
		case l.conns <- conn:
		case <-l.done:
			_ = conn.Close()
		}
	})

	l.server = &http.Server{Handler: mux}
	go func() {
		err := l.server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case l.errCh <- err:
			default:
			}
		}
		close(l.conns)
	}()

	return l, nil
}

func DialWebSocket(ctx context.Context, rawURL string, headers http.Header) (*WebSocketConnection, error) {
	return dialWebSocket(ctx, rawURL, headers, defaultReadTimeout, defaultWriteTimeout)
}

// AcceptWebSocket upgrades the HTTP request to a WebSocket connection.
// Origin validation is intentionally disabled (InsecureSkipVerify) because
// this is a tunnel transport: the server accepts connections from tunnel
// clients whose origin is not a browser page. Callers that need CSRF
// protection should validate the Origin header before calling this function.
func AcceptWebSocket(w http.ResponseWriter, r *http.Request) (*WebSocketConnection, error) {
	return acceptWebSocket(w, r, defaultReadTimeout, defaultWriteTimeout)
}

const defaultHandshakeTimeout = 15 * time.Second

func dialWebSocket(
	ctx context.Context,
	rawURL string,
	headers http.Header,
	readTimeout time.Duration,
	writeTimeout time.Duration,
) (*WebSocketConnection, error) {
	// Ensure a deadline exists so the handshake can't hang indefinitely.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultHandshakeTimeout)
		defer cancel()
	}
	dialOpts := &websocket.DialOptions{HTTPHeader: headers}
	wsConn, resp, err := websocket.Dial(ctx, rawURL, dialOpts)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial websocket failed with status %d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("dial websocket: %w", err)
	}

	remoteAddr := rawURL
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
		remoteAddr = parsed.Host
	}

	return newWebSocketConnection(wsConn, remoteAddr, readTimeout, writeTimeout), nil
}

func acceptWebSocket(
	w http.ResponseWriter,
	r *http.Request,
	readTimeout time.Duration,
	writeTimeout time.Duration,
) (*WebSocketConnection, error) {
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return nil, fmt.Errorf("accept websocket: %w", err)
	}
	return newWebSocketConnection(wsConn, r.RemoteAddr, readTimeout, writeTimeout), nil
}

type WebSocketConnection struct {
	conn         *websocket.Conn
	remoteAddr   string
	readTimeout  time.Duration
	writeTimeout time.Duration
	Encrypted    bool // reserved for future end-to-end encryption negotiation
	writeMu      sync.Mutex
	lastActivity atomic.Int64 // unix nano timestamp of last send or receive
}

type SendTiming struct {
	WriteLockWait  time.Duration
	FrameEncode    time.Duration
	WebSocketWrite time.Duration
}

func (t SendTiming) Total() time.Duration {
	return t.WriteLockWait + t.FrameEncode + t.WebSocketWrite
}

func newWebSocketConnection(conn *websocket.Conn, remoteAddr string, readTimeout, writeTimeout time.Duration) *WebSocketConnection {
	// nhooyr websocket defaults to a low read limit (~32KiB), which is too small
	// for proxied HTTP chunks and causes read-limited disconnects.
	conn.SetReadLimit(defaultReadLimit)

	c := &WebSocketConnection{
		conn:         conn,
		remoteAddr:   remoteAddr,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
	}
	c.touchActivity()
	return c
}

func (c *WebSocketConnection) Send(msg Message) error {
	_, err := c.SendWithTiming(msg)
	return err
}

func (c *WebSocketConnection) SendWithTiming(msg Message) (SendTiming, error) {
	var timing SendTiming

	lockWaitStartedAt := time.Now()
	c.writeMu.Lock()
	timing.WriteLockWait = time.Since(lockWaitStartedAt)
	defer c.writeMu.Unlock()

	frameEncodeStartedAt := time.Now()
	frame, err := NewFrame(msg)
	if err != nil {
		timing.FrameEncode = time.Since(frameEncodeStartedAt)
		return timing, fmt.Errorf("build websocket frame: %w", err)
	}
	payload, err := frame.MarshalBinary()
	if err != nil {
		timing.FrameEncode = time.Since(frameEncodeStartedAt)
		return timing, fmt.Errorf("encode websocket frame: %w", err)
	}
	timing.FrameEncode = time.Since(frameEncodeStartedAt)

	ctx, cancel := context.WithTimeout(context.Background(), c.writeTimeout)
	defer cancel()

	websocketWriteStartedAt := time.Now()
	if err := c.conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		timing.WebSocketWrite = time.Since(websocketWriteStartedAt)
		return timing, fmt.Errorf("send websocket message: %w", err)
	}
	timing.WebSocketWrite = time.Since(websocketWriteStartedAt)
	c.touchActivity()
	return timing, nil
}

func (c *WebSocketConnection) Receive() (Message, error) {
	return c.ReceiveContext(context.Background())
}

func (c *WebSocketConnection) ReceiveContext(parent context.Context) (Message, error) {
	ctx := parent
	cancel := func() {}
	if c.readTimeout > 0 {
		ctx, cancel = context.WithTimeout(parent, c.readTimeout)
	}
	defer cancel()

	msgType, payload, err := c.conn.Read(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("receive websocket message: %w", err)
	}
	if msgType != websocket.MessageBinary {
		return Message{}, fmt.Errorf("receive websocket message: expected binary frame, got type %d", msgType)
	}
	msg, err := decodeMessagePayload(payload)
	if err != nil {
		return Message{}, fmt.Errorf("receive websocket message: %w", err)
	}
	c.touchActivity()
	return msg, nil
}

func (c *WebSocketConnection) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *WebSocketConnection) RemoteAddr() string {
	return c.remoteAddr
}

// IdleDuration returns how long the connection has been idle (no sends or receives).
func (c *WebSocketConnection) IdleDuration() time.Duration {
	last := c.lastActivity.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}

func (c *WebSocketConnection) touchActivity() {
	c.lastActivity.Store(time.Now().UnixNano())
}

type websocketListener struct {
	ln     net.Listener
	server *http.Server
	conns  chan Connection
	errCh  chan error
	done   chan struct{}
	once   sync.Once
}

func (l *websocketListener) Accept() (Connection, error) {
	select {
	case conn, ok := <-l.conns:
		if !ok {
			select {
			case err := <-l.errCh:
				if err != nil {
					return nil, err
				}
			default:
			}
			return nil, net.ErrClosed
		}
		return conn, nil
	case err := <-l.errCh:
		if err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	}
}

func (l *websocketListener) Close() error {
	var closeErr error
	l.once.Do(func() {
		close(l.done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closeErr = l.server.Shutdown(ctx)
		if err := l.ln.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func (t *WebSocketTransport) readTimeout() time.Duration {
	if t.ReadTimeout <= 0 {
		return defaultReadTimeout
	}
	return t.ReadTimeout
}

func (t *WebSocketTransport) writeTimeout() time.Duration {
	if t.WriteTimeout <= 0 {
		return defaultWriteTimeout
	}
	return t.WriteTimeout
}
