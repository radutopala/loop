//go:build linux

package agentgate

// DefaultFileCacheSize caps the per-container FileHandler LRU. 1024 entries
// covers the decision cache for a long-lived claude session; callers can
// wire a custom size by building the Server struct directly.
const DefaultFileCacheSize = 1024

// NewServer stitches together the canonical production handler set for the
// given seccomp notify fd. One Server per container; handlers hold a
// per-container approver so prompts route to the right channel.
//
// notifyFD ownership transfers to the returned Server — Server.Transport
// closes it on shutdown via NotifyTransport.Close.
//
// auditor is fanned out into every handler. Pass NopAuditor{} to disable
// audit logging; a real FileAuditor is wired by the syscallwrap parent.
func NewServer(policy *Policy, approver Approver, auditor Auditor, channelID string, notifyFD int) *Server {
	if auditor == nil {
		auditor = NopAuditor{}
	}
	exec := NewExecveHandler(policy, approver)
	exec.Auditor = auditor
	conn := NewConnectHandler(policy, approver)
	conn.Auditor = auditor
	file := NewFileHandler(policy, approver, DefaultFileCacheSize)
	file.Auditor = auditor
	return &Server{
		Transport: NewNotifyTransport(notifyFD),
		Factory:   NewProcTraceeFactory(),
		Execve:    exec,
		Connect:   conn,
		File:      file,
		ChannelID: channelID,
	}
}
