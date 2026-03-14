package terminal

import (
	"os/exec"
	"sync"
)

// HostExecClient implements ExecClient for running shell commands directly
// on the host machine. On Unix it uses creack/pty; on Windows it uses ConPTY.
// The containerID parameter in ExecCreate is repurposed as the working directory.
type HostExecClient struct {
	mu    sync.Mutex
	execs map[string]*hostExec
}

// NewHostExecClient creates a new HostExecClient.
func NewHostExecClient() *HostExecClient {
	return &HostExecClient{
		execs: make(map[string]*hostExec),
	}
}

// lookPath wraps exec.LookPath for testing.
var lookPath = exec.LookPath
