package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

const frameHeaderSize = 9

// Frame is the binary tunnel transport unit:
// [1 byte type][4 bytes metadata length][4 bytes body length][metadata][body].
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
	Method          string              `json:"method,omitempty"`
	Path            string              `json:"path,omitempty"`
	Headers         map[string][]string `json:"headers,omitempty"`
	Status          int                 `json:"status,omitempty"`
	EndStream       bool                `json:"end_stream,omitempty"`
	Error           string              `json:"error,omitempty"`
	WSBinary        bool                `json:"ws_binary,omitempty"`
	Encrypted       bool                `json:"encrypted,omitempty"`
}

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

func UnmarshalFrame(payload []byte) (Frame, error) {
	if len(payload) < frameHeaderSize {
		return Frame{}, fmt.Errorf("frame too short: %d bytes", len(payload))
	}

	headerLen := int(binary.BigEndian.Uint32(payload[1:5]))
	bodyLen := int(binary.BigEndian.Uint32(payload[5:9]))
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

func encodeFrameMetadata(msg Message) ([]byte, error) {
	meta := frameMetadata{
		Type:            msg.Type,
		ID:              msg.ID,
		Token:           msg.Token,
		Subdomain:       msg.Subdomain,
		ProtocolVersion: msg.ProtocolVersion,
		SessionID:       msg.SessionID,
		MaxConnections:  msg.MaxConnections,
		Method:          msg.Method,
		Path:            msg.Path,
		Headers:         msg.Headers,
		Status:          msg.Status,
		EndStream:       msg.EndStream,
		Error:           msg.Error,
		WSBinary:        msg.WSBinary,
		Encrypted:       msg.Encrypted,
	}

	header, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encode frame metadata: %w", err)
	}
	return header, nil
}

func decodeFrameMetadata(header []byte) (Message, error) {
	if len(header) == 0 {
		return Message{}, nil
	}

	var meta frameMetadata
	if err := json.Unmarshal(header, &meta); err != nil {
		return Message{}, fmt.Errorf("decode frame metadata: %w", err)
	}
	return Message{
		Type:            meta.Type,
		ID:              meta.ID,
		Token:           meta.Token,
		Subdomain:       meta.Subdomain,
		ProtocolVersion: meta.ProtocolVersion,
		SessionID:       meta.SessionID,
		MaxConnections:  meta.MaxConnections,
		Method:          meta.Method,
		Path:            meta.Path,
		Headers:         meta.Headers,
		Status:          meta.Status,
		EndStream:       meta.EndStream,
		Error:           meta.Error,
		WSBinary:        meta.WSBinary,
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
