package tunnel

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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

func TestDecodeMessagePayload_AllowsHTTPRequestContinuationChunk(t *testing.T) {
	msg := Message{
		Type: MsgHTTPRequest,
		ID:   "req-continuation",
		Body: []byte("chunk-2"),
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
	if got.Type != MsgHTTPRequest {
		t.Fatalf("type = %d, want %d", got.Type, MsgHTTPRequest)
	}
	if got.ID != msg.ID {
		t.Fatalf("id = %q, want %q", got.ID, msg.ID)
	}
	if !bytes.Equal(got.Body, msg.Body) {
		t.Fatalf("body mismatch: got %q, want %q", got.Body, msg.Body)
	}

	if err := got.Validate(); err == nil {
		t.Fatal("expected application-level validation to reject continuation chunk")
	}
}
