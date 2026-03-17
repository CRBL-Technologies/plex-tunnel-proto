package tunnel

import (
	"bytes"
	"encoding/json"
	"testing"
)

type legacyJSONMessage struct {
	Type      MessageType         `json:"type"`
	ID        string              `json:"id,omitempty"`
	Method    string              `json:"method,omitempty"`
	Path      string              `json:"path,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Status    int                 `json:"status,omitempty"`
	EndStream bool                `json:"end_stream,omitempty"`
	Body      []byte              `json:"body,omitempty"`
}

func BenchmarkBinaryFrameVsLegacyJSON(b *testing.B) {
	body := bytes.Repeat([]byte("0123456789abcdef"), 16*1024) // 256 KiB payload

	msg := Message{
		Type:      MsgHTTPResponse,
		ID:        "req-1",
		Status:    200,
		Headers:   map[string][]string{"Content-Type": {"video/mp4"}},
		EndStream: true,
		Body:      body,
	}
	legacy := legacyJSONMessage{
		Type:      msg.Type,
		ID:        msg.ID,
		Status:    msg.Status,
		Headers:   msg.Headers,
		EndStream: msg.EndStream,
		Body:      msg.Body,
	}

	b.Run("binary-frame-encode-decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			frame, err := NewFrame(msg)
			if err != nil {
				b.Fatalf("new frame: %v", err)
			}
			payload, err := frame.MarshalBinary()
			if err != nil {
				b.Fatalf("marshal frame: %v", err)
			}
			decoded, err := decodeMessagePayload(payload)
			if err != nil {
				b.Fatalf("decode frame: %v", err)
			}
			if len(decoded.Body) != len(body) {
				b.Fatalf("decoded body length = %d, want %d", len(decoded.Body), len(body))
			}
		}
	})

	b.Run("legacy-json-encode-decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			payload, err := json.Marshal(legacy)
			if err != nil {
				b.Fatalf("marshal json: %v", err)
			}
			var decoded legacyJSONMessage
			if err := json.Unmarshal(payload, &decoded); err != nil {
				b.Fatalf("unmarshal json: %v", err)
			}
			if len(decoded.Body) != len(body) {
				b.Fatalf("decoded body length = %d, want %d", len(decoded.Body), len(body))
			}
		}
	})
}
