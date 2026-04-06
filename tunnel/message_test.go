package tunnel

import "testing"

func TestMsgMaxConnectionsUpdateValidate(t *testing.T) {
	tests := []struct {
		name    string
		msg     Message
		wantErr bool
	}{
		{
			name: "valid",
			msg: Message{
				Type:           MsgMaxConnectionsUpdate,
				MaxConnections: 4,
			},
		},
		{
			name: "zero max connections invalid",
			msg: Message{
				Type:           MsgMaxConnectionsUpdate,
				MaxConnections: 0,
			},
			wantErr: true,
		},
		{
			name: "negative max connections invalid",
			msg: Message{
				Type:           MsgMaxConnectionsUpdate,
				MaxConnections: -1,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestMsgCancelValidate(t *testing.T) {
	tests := []struct {
		name    string
		msg     Message
		wantErr bool
	}{
		{
			name: "valid cancel",
			msg: Message{
				Type: MsgCancel,
				ID:   "req-123",
			},
		},
		{
			name: "missing id invalid",
			msg: Message{
				Type: MsgCancel,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestMsgRegisterAckValidateProtocolVersion(t *testing.T) {
	tests := []struct {
		name    string
		version uint16
		wantErr bool
	}{
		{
			name:    "current protocol version valid",
			version: ProtocolVersion,
		},
		{
			name:    "zero protocol version invalid",
			version: 0,
			wantErr: true,
		},
		{
			name:    "explicit v1 downgrade invalid",
			version: 1,
			wantErr: true,
		},
		{
			name:    "protocol version minus one invalid",
			version: ProtocolVersion - 1,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := Message{
				Type:            MsgRegisterAck,
				Subdomain:       "app",
				ProtocolVersion: tc.version,
				SessionID:       "sess-123",
				MaxConnections:  4,
			}

			err := msg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}
