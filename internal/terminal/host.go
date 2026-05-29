package terminal

import (
	"crypto/rand"
	"os/exec"
	"sync"
)

// hasEnvKey reports whether the env slice contains a variable with the given key.
func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// HostExecClient implements ExecClient for running shell commands directly
// on the host machine. On Unix it uses creack/pty; on Windows it uses ConPTY.
// The containerID parameter in ExecCreate is repurposed as the working directory.
type HostExecClient struct {
	mu               sync.Mutex
	execs            map[string]*hostExec
	lookPath         func(file string) (string, error)
	randRead         func([]byte) (int, error)
	defaultShell     func() string
	defaultShellArgs func() []string
	hostPlatform
}

// NewHostExecClient creates a new HostExecClient.
func NewHostExecClient() *HostExecClient {
	c := &HostExecClient{
		execs:    make(map[string]*hostExec),
		lookPath: exec.LookPath,
		randRead: rand.Read,
	}
	platformDefaults(c)
	return c
}
