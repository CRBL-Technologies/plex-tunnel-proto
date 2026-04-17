package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

const frameHeaderSize = 9

// Frame is the binary tunnel transport unit.
//
// Wire format (big-endian / network byte order):
//
//	Offset  Size  Field
//	─────────────────────────────────────
//	 0       1    Message type (uint8)
//	 1       4    Metadata length in bytes (uint32, big-endian)
//	 5       4    Body length in bytes (uint32, big-endian)
//	 9       N    JSON-encoded metadata (N = metadata length)
//	 9+N     M    Binary body (M = body length)
//
// Total frame size = 9 + N + M bytes.
//
// The message type byte doubles as an implicit version indicator: valid
// types are in [1, 14] for protocol v3 with CapWSFlowControl. A receiver that
// encounters a type outside this range should treat the frame as incompatible.
//
// There is no explicit per-frame version field. Forward compatibility is
// handled at the protocol level via the ProtocolVersion negotiated during
// the Register/RegisterAck handshake. If the wire format changes in a
// future protocol version, the handshake will reject incompatible peers
// before any data frames are exchanged.
type Frame struct {
	Type   MessageType
	Header []byte
	Body   []byte
}

type frameMetadata struct {
	Type            MessageType         `json:"type,omitempty"`
	ID              string              `json:"id,omitempty"`
	Token           string              `json:"token,omitempty"`
	Subdomain       string              `json:"subdomain,omitempty"`
	ProtocolVersion uint16              `json:"protocol_version,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	MaxConnections  int                 `json:"max_connections,omitempty"`
	Capabilities    uint32              `json:"capabilities,omitempty"`
	Method          string              `json:"method,omitempty"`
	Path            string              `json:"path,omitempty"`
	Headers         map[string][]string `json:"headers,omitempty"`
	Status          int                 `json:"status,omitempty"`
	EndStream       bool                `json:"end_stream,omitempty"`
	Error           string              `json:"error,omitempty"`
	WSBinary        bool                `json:"ws_binary,omitempty"`
	WindowIncrement uint32              `json:"window_increment,omitempty"`
	Encrypted       bool                `json:"encrypted,omitempty"`
}

// NewFrame encodes a Message into a Frame ready for binary serialization.
// Returns an error if the metadata or body exceeds size limits.
func NewFrame(msg Message) (Frame, error) {
	header, err := encodeFrameMetadata(msg)
	if err != nil {
		return Frame{}, err
	}
	if len(header) > math.MaxUint32 {
		return Frame{}, fmt.Errorf("frame metadata too large: %d bytes", len(header))
	}
	if len(msg.Body) > math.MaxUint32 {
		return Frame{}, fmt.Errorf("frame body too large: %d bytes", len(msg.Body))
	}
	return Frame{
		Type:   msg.Type,
		Header: header,
		Body:   msg.Body,
	}, nil
}

// MarshalBinary serializes the frame into the wire format.
func (f Frame) MarshalBinary() ([]byte, error) {
	// Frame is exported, so callers may construct it directly without NewFrame.
	// Keep length guards here as a final validation layer.
	if len(f.Header) > math.MaxUint32 {
		return nil, fmt.Errorf("frame metadata too large: %d bytes", len(f.Header))
	}
	if len(f.Body) > math.MaxUint32 {
		return nil, fmt.Errorf("frame body too large: %d bytes", len(f.Body))
	}

	payload := make([]byte, frameHeaderSize+len(f.Header)+len(f.Body))
	payload[0] = byte(f.Type)
	binary.BigEndian.PutUint32(payload[1:5], uint32(len(f.Header)))
	binary.BigEndian.PutUint32(payload[5:9], uint32(len(f.Body)))
	copy(payload[frameHeaderSize:], f.Header)
	copy(payload[frameHeaderSize+len(f.Header):], f.Body)
	return payload, nil
}

// maxFrameComponentSize limits individual header/body lengths so that
// frameHeaderSize + headerLen + bodyLen cannot overflow int on 32-bit
// GOARCH (where int is int32). Two components at this cap plus the 9-byte
// frame header stay within math.MaxInt32.
const maxFrameComponentSize = (math.MaxInt32 - frameHeaderSize) / 2

// UnmarshalFrame parses a wire-format payload into a Frame.
func UnmarshalFrame(payload []byte) (Frame, error) {
	if len(payload) < frameHeaderSize {
		return Frame{}, fmt.Errorf("frame too short: %d bytes", len(payload))
	}

	headerLen := int(binary.BigEndian.Uint32(payload[1:5]))
	bodyLen := int(binary.BigEndian.Uint32(payload[5:9]))
	if headerLen < 0 || headerLen > maxFrameComponentSize || bodyLen < 0 || bodyLen > maxFrameComponentSize {
		return Frame{}, fmt.Errorf("frame component length out of range: header=%d body=%d", headerLen, bodyLen)
	}
	expectedLen := frameHeaderSize + headerLen + bodyLen
	if expectedLen != len(payload) {
		return Frame{}, fmt.Errorf(
			"frame length mismatch: got %d bytes, expected %d",
			len(payload),
			expectedLen,
		)
	}

	headerStart := frameHeaderSize
	headerEnd := headerStart + headerLen
	header := make([]byte, headerLen)
	copy(header, payload[headerStart:headerEnd])
	body := make([]byte, bodyLen)
	copy(body, payload[headerEnd:])

	return Frame{
		Type:   MessageType(payload[0]),
		Header: header,
		Body:   body,
	}, nil
}

func (f Frame) Message() (Message, error) {
	msg, err := decodeFrameMetadata(f.Header)
	if err != nil {
		return Message{}, err
	}
	if msg.Type != 0 && msg.Type != f.Type {
		return Message{}, fmt.Errorf(
			"frame type mismatch: frame=%d metadata=%d",
			f.Type,
			msg.Type,
		)
	}

	msg.Type = f.Type
	msg.Body = f.Body
	return msg, nil
}

// maxMetadataSize limits the JSON metadata to 1 MB to prevent unbounded
// memory allocation from large Headers maps or other metadata fields.
const maxMetadataSize = 1 * 1024 * 1024

// maxHeaderEntries limits the number of header keys to prevent OOM from
// an attacker sending millions of tiny headers.
const maxHeaderEntries = 256

func encodeFrameMetadata(msg Message) ([]byte, error) {
	if len(msg.Headers) > maxHeaderEntries {
		return nil, fmt.Errorf("too many header entries: %d (max %d)", len(msg.Headers), maxHeaderEntries)
	}
	meta := frameMetadata{
		Type:            msg.Type,
		ID:              msg.ID,
		Token:           msg.Token,
		Subdomain:       msg.Subdomain,
		ProtocolVersion: msg.ProtocolVersion,
		SessionID:       msg.SessionID,
		MaxConnections:  msg.MaxConnections,
		Capabilities:    msg.Capabilities,
		Method:          msg.Method,
		Path:            msg.Path,
		Headers:         msg.Headers,
		Status:          msg.Status,
		EndStream:       msg.EndStream,
		Error:           msg.Error,
		WSBinary:        msg.WSBinary,
		WindowIncrement: msg.WindowIncrement,
		Encrypted:       msg.Encrypted,
	}

	header, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encode frame metadata: %w", err)
	}
	if len(header) > maxMetadataSize {
		return nil, fmt.Errorf("frame metadata too large: %d bytes (max %d)", len(header), maxMetadataSize)
	}
	return header, nil
}

func decodeFrameMetadata(header []byte) (Message, error) {
	if len(header) == 0 {
		return Message{}, nil
	}
	if len(header) > maxMetadataSize {
		return Message{}, fmt.Errorf("frame metadata too large: %d bytes (max %d)", len(header), maxMetadataSize)
	}

	var meta frameMetadata
	if err := json.Unmarshal(header, &meta); err != nil {
		return Message{}, fmt.Errorf("decode frame metadata: invalid metadata")
	}
	if len(meta.Headers) > maxHeaderEntries {
		return Message{}, fmt.Errorf("too many header entries: %d (max %d)", len(meta.Headers), maxHeaderEntries)
	}
	return Message{
		Type:            meta.Type,
		ID:              meta.ID,
		Token:           meta.Token,
		Subdomain:       meta.Subdomain,
		ProtocolVersion: meta.ProtocolVersion,
		SessionID:       meta.SessionID,
		MaxConnections:  meta.MaxConnections,
		Capabilities:    meta.Capabilities,
		Method:          meta.Method,
		Path:            meta.Path,
		Headers:         meta.Headers,
		Status:          meta.Status,
		EndStream:       meta.EndStream,
		Error:           meta.Error,
		WSBinary:        meta.WSBinary,
		WindowIncrement: meta.WindowIncrement,
		Encrypted:       meta.Encrypted,
	}, nil
}

func decodeMessagePayload(payload []byte) (Message, error) {
	frame, err := UnmarshalFrame(payload)
	if err != nil {
		return Message{}, fmt.Errorf("decode frame: %w", err)
	}
	msg, err := frame.Message()
	if err != nil {
		return Message{}, fmt.Errorf("decode frame message: %w", err)
	}
	return msg, nil
}
