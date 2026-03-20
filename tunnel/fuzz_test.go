package tunnel

import "testing"

func FuzzMessageValidate(f *testing.F) {
	f.Add(uint8(MsgPing), "", "", "", uint16(0), "", 0, "", "", 0, false, []byte(nil))
	f.Add(uint8(MsgRegister), "", "tok", "app", ProtocolVersion, "", 4, "", "", 0, false, []byte(nil))
	f.Add(uint8(MsgRegisterAck), "", "", "app", ProtocolVersion, "sess-1", 4, "", "", 0, false, []byte(nil))
	f.Add(uint8(MsgHTTPRequest), "req-1", "", "", uint16(0), "", 0, "GET", "/library", 0, true, []byte("body"))
	f.Add(uint8(MsgHTTPResponse), "req-1", "", "", uint16(0), "", 0, "", "", 200, true, []byte("resp"))

	f.Fuzz(func(
		t *testing.T,
		typ uint8,
		id string,
		token string,
		subdomain string,
		protocolVersion uint16,
		sessionID string,
		maxConnections int,
		method string,
		path string,
		status int,
		endStream bool,
		body []byte,
	) {
		msg := Message{
			Type:            MessageType(typ),
			ID:              id,
			Token:           token,
			Subdomain:       subdomain,
			ProtocolVersion: protocolVersion,
			SessionID:       sessionID,
			MaxConnections:  maxConnections,
			Method:          method,
			Path:            path,
			Status:          status,
			EndStream:       endStream,
			Body:            body,
		}
		_ = msg.Validate()
	})
}

func FuzzFrameMetadataJSON(f *testing.F) {
	seeds := []Message{
		{Type: MsgRegister, Token: "tok", Subdomain: "app", ProtocolVersion: ProtocolVersion, MaxConnections: 4},
		{Type: MsgRegisterAck, Subdomain: "app", ProtocolVersion: ProtocolVersion, SessionID: "sess-1", MaxConnections: 4},
		{Type: MsgHTTPRequest, ID: "req-1", Method: "POST", Path: "/upload", EndStream: true},
		{Type: MsgHTTPResponse, ID: "req-1", Status: 200, EndStream: true},
		{Type: MsgWSOpen, ID: "ws-1", Path: "/socket"},
		{Type: MsgWSFrame, ID: "ws-1", WSBinary: true},
		{Type: MsgWSClose, ID: "ws-1"},
		{Type: MsgError, Error: "boom"},
	}
	for _, seed := range seeds {
		data, err := encodeFrameMetadata(seed)
		if err != nil {
			f.Fatalf("marshal seed metadata: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte("{}"))
	f.Add([]byte("not-json"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = decodeFrameMetadata(data)
	})
}

func FuzzFrameDecode(f *testing.F) {
	seeds := []Message{
		{Type: MsgPing},
		{Type: MsgRegister, Token: "tok", Subdomain: "app", ProtocolVersion: ProtocolVersion, MaxConnections: 4},
		{Type: MsgRegisterAck, Subdomain: "app", ProtocolVersion: ProtocolVersion, SessionID: "sess-1", MaxConnections: 4},
		{Type: MsgHTTPRequest, ID: "req-1", Method: "POST", Path: "/upload", Body: []byte("payload"), EndStream: true},
		{Type: MsgHTTPResponse, ID: "req-1", Status: 200, Body: []byte("ok"), EndStream: true},
		{Type: MsgWSFrame, ID: "ws-1", Body: []byte{0x00, 0x01, 0x02}, WSBinary: true},
		{Type: MsgError, Error: "boom"},
	}
	for _, seed := range seeds {
		frame, err := NewFrame(seed)
		if err != nil {
			f.Fatalf("new frame seed: %v", err)
		}
		payload, err := frame.MarshalBinary()
		if err != nil {
			f.Fatalf("marshal frame seed: %v", err)
		}
		f.Add(payload)
	}
	f.Add([]byte{})
	f.Add([]byte{byte(MsgPing), 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{byte(MsgHTTPRequest), 0, 0, 0, 1, 0, 0, 0, 0, '{'})

	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 1<<20 {
			t.Skip()
		}
		_, _ = decodeMessagePayload(payload)
	})
}
