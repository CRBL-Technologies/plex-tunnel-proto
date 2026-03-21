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
