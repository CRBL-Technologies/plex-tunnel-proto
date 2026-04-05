package tunnel

import "context"

// Transport abstracts the underlying connection mechanism for the tunnel protocol.
type Transport interface {
	Dial(ctx context.Context, url string) (Connection, error)
	Listen(addr string) (Listener, error)
}

// Connection is a bidirectional tunnel message stream.
type Connection interface {
	Send(msg Message) error
	Receive() (Message, error)
	Close() error
	RemoteAddr() string
}

// Listener accepts incoming tunnel connections.
type Listener interface {
	Accept() (Connection, error)
	Close() error
}
