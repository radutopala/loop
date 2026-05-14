//go:build linux

package agentgate

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

// TestNewServerWiresHandlers sanity-checks the struct assembly: the returned
// Server has every field populated (no nil transport/factory/handlers) so
// Run() won't hit a nil-deref on the first trap.
func TestNewServerWiresHandlers(t *testing.T) {
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil, nil)
	require.NoError(t, err)
	peer := func(int) string { return "terminal:leaf-X" }
	srv := NewServer(policy, &stubApprover{}, NopAuditor{}, peer, "ch-1", -1)
	require.NotNil(t, srv)
	require.NotNil(t, srv.Transport)
	require.NotNil(t, srv.Factory)
	require.NotNil(t, srv.Execve)
	require.NotNil(t, srv.Connect)
	require.NotNil(t, srv.File)
	require.Equal(t, "ch-1", srv.ChannelID)
	// Every handler picked up the passed auditor, not a fresh default.
	require.Equal(t, NopAuditor{}, srv.Execve.Auditor)
	require.Equal(t, NopAuditor{}, srv.Connect.Auditor)
	require.Equal(t, NopAuditor{}, srv.File.Auditor)
	// PeerSource is fanned out to every handler so terminal-pane
	// attribution works regardless of which syscall trapped.
	require.NotNil(t, srv.Execve.PeerSource)
	require.NotNil(t, srv.Connect.PeerSource)
	require.NotNil(t, srv.File.PeerSource)
	require.Equal(t, "terminal:leaf-X", srv.Execve.PeerSource(1))
	require.Equal(t, "terminal:leaf-X", srv.Connect.PeerSource(1))
	require.Equal(t, "terminal:leaf-X", srv.File.PeerSource(1))
}

// TestNewServerNilAuditorDefaultsToNop covers the nil-auditor guard: callers
// passing a nil Auditor get NopAuditor{} implicitly so handlers never panic
// on Write.
func TestNewServerNilAuditorDefaultsToNop(t *testing.T) {
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil, nil)
	require.NoError(t, err)
	srv := NewServer(policy, &stubApprover{}, nil, nil, "ch-1", -1)
	require.NotNil(t, srv)
	require.Equal(t, NopAuditor{}, srv.Execve.Auditor)
	require.Equal(t, NopAuditor{}, srv.Connect.Auditor)
	require.Equal(t, NopAuditor{}, srv.File.Auditor)
	// nil PeerSource leaves each handler's field nil — Handle still routes
	// to "chat" via sourceForPID's lookup==nil branch.
	require.Nil(t, srv.Execve.PeerSource)
	require.Nil(t, srv.Connect.PeerSource)
	require.Nil(t, srv.File.PeerSource)
}
