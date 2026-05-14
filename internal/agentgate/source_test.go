package agentgate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceForPID covers every branch of the canonical PID→Source mapping.
// Wired into ExecveHandler/ConnectHandler/FileHandler via h.PeerSource — the
// table mirrors the cases each handler may see at runtime so the wire-format
// Source value is consistent across surfaces.
func TestSourceForPID(t *testing.T) {
	tests := []struct {
		name   string
		pid    int
		lookup PeerSourceLookup
		want   string
	}{
		{
			name:   "zero PID short-circuits to chat",
			pid:    0,
			lookup: func(int) string { return "terminal:leaf-X" },
			want:   "chat",
		},
		{
			name:   "negative PID short-circuits to chat",
			pid:    -1,
			lookup: func(int) string { return "terminal:leaf-X" },
			want:   "chat",
		},
		{
			name:   "nil lookup short-circuits to chat",
			pid:    1234,
			lookup: nil,
			want:   "chat",
		},
		{
			name:   "lookup returning empty falls back to chat",
			pid:    1234,
			lookup: func(int) string { return "" },
			want:   "chat",
		},
		{
			name:   "lookup returning terminal leaf is passed through",
			pid:    1234,
			lookup: func(int) string { return "terminal:leaf-X" },
			want:   "terminal:leaf-X",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sourceForPID(tc.pid, tc.lookup))
		})
	}
}
