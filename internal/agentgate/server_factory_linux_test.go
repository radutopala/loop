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
	srv := NewServer(policy, &stubApprover{}, NopAuditor{}, "ch-1", -1)
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
}

// TestNewServerNilAuditorDefaultsToNop covers the nil-auditor guard: callers
// passing a nil Auditor get NopAuditor{} implicitly so handlers never panic
// on Write.
func TestNewServerNilAuditorDefaultsToNop(t *testing.T) {
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil, nil)
	require.NoError(t, err)
	srv := NewServer(policy, &stubApprover{}, nil, "ch-1", -1)
	require.NotNil(t, srv)
	require.Equal(t, NopAuditor{}, srv.Execve.Auditor)
	require.Equal(t, NopAuditor{}, srv.Connect.Auditor)
	require.Equal(t, NopAuditor{}, srv.File.Auditor)
}
