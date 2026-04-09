package tunnel

import (
	"strings"
	"testing"
)

func TestProtocolVersionIsThree(t *testing.T) {
	if ProtocolVersion != 3 {
		t.Fatalf("ProtocolVersion = %d, want 3", ProtocolVersion)
	}
}

func TestCapLeasedPoolBitIsOne(t *testing.T) {
	if CapLeasedPool != 1 {
		t.Fatalf("CapLeasedPool = %d, want 1", CapLeasedPool)
	}
}

func TestValidateRegisterAcceptsLeasedPoolCapability(t *testing.T) {
	msg := Message{
		Type:            MsgRegister,
		Token:           "token-123",
		ProtocolVersion: ProtocolVersion,
		MaxConnections:  4,
		Capabilities:    CapLeasedPool,
	}

	if err := msg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateRegisterIgnoresUnknownCapabilityBits(t *testing.T) {
	msg := Message{
		Type:            MsgRegister,
		Token:           "token-123",
		ProtocolVersion: ProtocolVersion,
		MaxConnections:  4,
		Capabilities:    0xFF00,
	}

	if err := msg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateRejectsOlderProtocolVersion(t *testing.T) {
	msg := Message{
		Type:            MsgRegister,
		Token:           "token-123",
		ProtocolVersion: 2,
		MaxConnections:  4,
	}

	err := msg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "minimum 3") {
		t.Fatalf("Validate() error = %q, want mention of minimum 3", err)
	}
}

func TestRegisterFrameRoundTripPreservesCapabilities(t *testing.T) {
	msg := Message{
		Type:            MsgRegister,
		Token:           "token-123",
		ProtocolVersion: ProtocolVersion,
		MaxConnections:  4,
		Capabilities:    CapLeasedPool,
	}

	frame, err := NewFrame(msg)
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}

	payload, err := frame.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	got, err := decodeMessagePayload(payload)
	if err != nil {
		t.Fatalf("decodeMessagePayload() error = %v", err)
	}

	if got.Capabilities != msg.Capabilities {
		t.Fatalf("Capabilities = %d, want %d", got.Capabilities, msg.Capabilities)
	}
}

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
