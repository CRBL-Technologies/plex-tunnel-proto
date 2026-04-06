package tunnel

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

func TestFrameMarshalUnmarshalRoundTrip(t *testing.T) {
	body := []byte{0x00, 0x7f, 0x80, 0xff}
	msg := Message{
		Type:      MsgHTTPResponse,
		ID:        "req-1",
		Status:    200,
		Headers:   map[string][]string{"Content-Type": {"video/mp4"}},
		Body:      body,
		EndStream: true,
	}

	frame, err := NewFrame(msg)
	if err != nil {
		t.Fatalf("new frame: %v", err)
	}

	payload, err := frame.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	if len(payload) < frameHeaderSize {
		t.Fatalf("payload too short: %d", len(payload))
	}
	if got := MessageType(payload[0]); got != msg.Type {
		t.Fatalf("type byte = %d, want %d", got, msg.Type)
	}

	headerLen := int(binary.BigEndian.Uint32(payload[1:5]))
	bodyLen := int(binary.BigEndian.Uint32(payload[5:9]))
	if bodyLen != len(body) {
		t.Fatalf("body length = %d, want %d", bodyLen, len(body))
	}
	if want := frameHeaderSize + headerLen + bodyLen; len(payload) != want {
		t.Fatalf("payload length = %d, want %d", len(payload), want)
	}

	header := payload[frameHeaderSize : frameHeaderSize+headerLen]
	var metadata map[string]any
	if err := json.Unmarshal(header, &metadata); err != nil {
		t.Fatalf("decode metadata json: %v", err)
	}
	if _, hasBody := metadata["body"]; hasBody {
		t.Fatalf("metadata should not contain body field: %s", string(header))
	}

	got, err := decodeMessagePayload(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Type != msg.Type {
		t.Fatalf("type = %d, want %d", got.Type, msg.Type)
	}
	if got.ID != msg.ID {
		t.Fatalf("id = %q, want %q", got.ID, msg.ID)
	}
	if got.Status != msg.Status {
		t.Fatalf("status = %d, want %d", got.Status, msg.Status)
	}
	if !got.EndStream {
		t.Fatal("expected EndStream=true")
	}
	if !bytes.Equal(got.Body, body) {
		t.Fatalf("body mismatch: got %v, want %v", got.Body, body)
	}
}

func TestFrameRegisterAckRoundTripWithSessionFields(t *testing.T) {
	msg := Message{
		Type:            MsgRegisterAck,
		Subdomain:       "app",
		ProtocolVersion: ProtocolVersion,
		SessionID:       "sess-123",
		MaxConnections:  4,
	}

	frame, err := NewFrame(msg)
	if err != nil {
		t.Fatalf("new frame: %v", err)
	}

	payload, err := frame.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	got, err := decodeMessagePayload(payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if got.Type != msg.Type {
		t.Fatalf("type = %d, want %d", got.Type, msg.Type)
	}
	if got.Subdomain != msg.Subdomain {
		t.Fatalf("subdomain = %q, want %q", got.Subdomain, msg.Subdomain)
	}
	if got.ProtocolVersion != msg.ProtocolVersion {
		t.Fatalf("protocol_version = %d, want %d", got.ProtocolVersion, msg.ProtocolVersion)
	}
	if got.SessionID != msg.SessionID {
		t.Fatalf("session_id = %q, want %q", got.SessionID, msg.SessionID)
	}
	if got.MaxConnections != msg.MaxConnections {
		t.Fatalf("max_connections = %d, want %d", got.MaxConnections, msg.MaxConnections)
	}
}

func TestFrameMessageTypeMismatch(t *testing.T) {
	msg := Message{Type: MsgPing}
	frame, err := NewFrame(msg)
	if err != nil {
		t.Fatalf("new frame: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(frame.Header, &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	meta["type"] = float64(MsgPong)
	mutatedHeader, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}

	frame.Header = mutatedHeader
	_, err = frame.Message()
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestUnmarshalFrameLengthMismatch(t *testing.T) {
	// Declares 10-byte metadata + 2-byte body but only provides one payload byte.
	payload := []byte{
		byte(MsgHTTPRequest),
		0, 0, 0, 10,
		0, 0, 0, 2,
		'{',
	}
	_, err := UnmarshalFrame(payload)
	if err == nil {
		t.Fatal("expected frame length mismatch error")
	}
}

func TestUnmarshalFrameCopiesPayload(t *testing.T) {
	msg := Message{
		Type:   MsgHTTPResponse,
		ID:     "copy-check",
		Status: 200,
		Body:   []byte("payload"),
	}
	frame, err := NewFrame(msg)
	if err != nil {
		t.Fatalf("new frame: %v", err)
	}
	payload, err := frame.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}

	decoded, err := UnmarshalFrame(payload)
	if err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}

	headerLen := int(binary.BigEndian.Uint32(payload[1:5]))
	bodyLen := int(binary.BigEndian.Uint32(payload[5:9]))
	headerStart := frameHeaderSize
	headerEnd := headerStart + headerLen
	bodyEnd := headerEnd + bodyLen

	for i := headerStart; i < bodyEnd; i++ {
		payload[i] = 0
	}

	if len(decoded.Header) == 0 {
		t.Fatal("expected decoded header")
	}
	if len(decoded.Body) == 0 {
		t.Fatal("expected decoded body")
	}
	if bytes.Equal(decoded.Header, make([]byte, len(decoded.Header))) {
		t.Fatal("decoded header unexpectedly changed after payload mutation")
	}
	if bytes.Equal(decoded.Body, make([]byte, len(decoded.Body))) {
		t.Fatal("decoded body unexpectedly changed after payload mutation")
	}
}

func TestMaxFrameComponentSizeBounds(t *testing.T) {
	// Two max-size components plus the frame header must not overflow int32.
	sum := int64(frameHeaderSize) + 2*int64(maxFrameComponentSize)
	if sum > int64(math.MaxInt32) {
		t.Fatalf("maxFrameComponentSize too large: 2*%d + %d = %d exceeds math.MaxInt32 (%d)",
			maxFrameComponentSize, frameHeaderSize, sum, math.MaxInt32)
	}
}

func TestUnmarshalFrameRejectsOversizeComponent(t *testing.T) {
	payload := make([]byte, frameHeaderSize)
	payload[0] = byte(MsgHTTPRequest)
	binary.BigEndian.PutUint32(payload[1:5], uint32(maxFrameComponentSize+1))
	binary.BigEndian.PutUint32(payload[5:9], 0)

	_, err := UnmarshalFrame(payload)
	if err == nil {
		t.Fatal("expected oversize component error")
	}
}
