package agent

import "strings"

// DiskFullNotice is what the chat shows when a run fails because Docker is
// out of disk space. The disk that fills is the Docker VM's, not the host's,
// so freeing host space doesn't help.
const DiskFullNotice = "Docker is out of disk space (no space left on device). " +
	"Free space in Docker — e.g. `docker builder prune -a` and removing unused images/containers — then send again."

// IsDiskFull reports whether a run error says Docker ran out of disk space:
// the daemon's API errors (container create, file copy) and the agent's own
// output (Claude Code failing to write its session files) both carry the
// ENOSPC text.
func IsDiskFull(errMsg string) bool {
	m := strings.ToLower(errMsg)
	return strings.Contains(m, "no space left on device") || strings.Contains(m, "enospc")
}
