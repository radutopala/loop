//go:build linux

package syscallwrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/httpapprover"
	"github.com/radutopala/loop/internal/types"
)

// runParent loads the gate policy, spawns the seccomp-installer child via a
// re-exec of /proc/self/exe, receives the notify fd from the child over a
// socketpair, and then runs agentgate.Server against that fd until either
// the child exits or a shutdown signal arrives.
//
// The parent stays as root (the uid entrypoint.sh invoked us as) so the
// non-root child/claude cannot signal it — see docs/gates.md
// "Parent-kill / orphan-fd attack" for the full rationale. The child is
// dropped to the agent uid via os.ProcAttr.Sys.Credential.
//
// Terminal-exec mode: when this is launched from an already-agent-uid shell
// (e.g. `docker exec` into the shell container and running
// `loop syscallwrap -- claude`), the Credential drop is skipped — non-root
// can't setuid anyway. pdeathsig still couples parent-death → child-death so
// the notify fd can't outlive the agentgate server; the weaker-uid-separation
// trade-off is documented in docs/gates.md.
func (a *app) runParent() error {
	// parseArgs fail-fast: the child needs a valid target too; no point
	// spawning only for the child to bounce on bad argv.
	if _, err := parseArgs(a.args); err != nil {
		return err
	}

	policyFile := a.getenv(envPolicyFile)
	if policyFile == "" {
		return errors.New(envPolicyFile + " is required")
	}
	apiURL := a.getenv(envAPIURL)
	if apiURL == "" {
		return errors.New(envAPIURL + " is required")
	}
	token := a.getenv(envToken)
	if token == "" {
		return errors.New(envToken + " is required")
	}
	hostUser := a.getenv(envHostUser)
	if hostUser == "" {
		return errors.New(envHostUser + " is required")
	}
	channelID := a.getenv(envChannelID)

	// Load + compile policy before anything irreversible. A broken policy
	// must not spawn a child — we'd have no gate to trap against.
	policy, err := loadGatePolicy(a.readFile, policyFile)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}

	uid, gid, err := a.lookupUser(hostUser)
	if err != nil {
		return fmt.Errorf("lookup user %q: %w", hostUser, err)
	}
	// Terminal-exec mode: already agent-uid, skip the Credential drop.
	// Sentinel (-1, -1) tells defaultStartChild to omit Credential so the
	// fork inherits the current uid instead of attempting a setuid that
	// would fail with EPERM from non-root.
	if a.getuid() == uid {
		uid, gid = -1, -1
	}

	parentFD, childFD, err := a.socketpair()
	if err != nil {
		return fmt.Errorf("socketpair: %w", err)
	}

	// Wrap both ends as os.File so we can pass childEnd into StartProcess
	// and FileConn parentEnd into a net.UnixConn.
	parentFile := os.NewFile(uintptr(parentFD), "loop-syscallwrap-parent")
	childFile := os.NewFile(uintptr(childFD), "loop-syscallwrap-child")

	// Build the child's env: strip any caller-provided mode, then append
	// mode=child. We pass through everything else so claude sees the same
	// environment it would see without the gate.
	childEnv := childProcessEnv(a.environ())

	proc, err := a.startChild(a.selfArgv, childEnv, childFile, uid, gid)
	// Whether startChild succeeded or not, the parent no longer needs its
	// copy of the child-end — the kernel dup'd it across fork (on success)
	// or we must release it (on failure).
	_ = childFile.Close()
	if err != nil {
		_ = parentFile.Close()
		return fmt.Errorf("start child: %w", err)
	}

	parentConn, err := net.FileConn(parentFile)
	// FileConn dup'd the fd; close our File copy unconditionally.
	_ = parentFile.Close()
	if err != nil {
		// Child is already running but we can't talk to it. Best-effort
		// kill so PDEATHSIG doesn't sit as the child's only exit route.
		_ = proc.Kill()
		_, _ = a.waitChild(proc)
		return fmt.Errorf("wrap parent fd: %w", err)
	}
	uc, ok := parentConn.(*net.UnixConn)
	if !ok {
		_ = parentConn.Close()
		_ = proc.Kill()
		_, _ = a.waitChild(proc)
		return fmt.Errorf("parent fd is not a unix conn: %T", parentConn)
	}

	// Receive handshake (channel-id + notify fd). The child blocks on ack
	// just after, so the sequence is: recv-HS → newGateServer → sendAck.
	// Building the server before sending ack guarantees Run() is ready to
	// service the first trap.
	hsChannelID, notifyFD, err := a.receiveHS(uc)
	if err != nil {
		_ = uc.Close()
		_ = proc.Kill()
		_, _ = a.waitChild(proc)
		return fmt.Errorf("receive handshake: %w", err)
	}

	// The child echoes LOOP_CHANNEL_ID in the handshake body. We trust the
	// env over the wire if present — channelID from env is authoritative.
	if channelID == "" {
		channelID = hsChannelID
	}

	auditor, err := a.openAuditor(a.getenv(envAuditDir), parseAuditRetention(a.getenv(envAuditRetentionDays)), a.getenv(envAuditVerbose) == "1")
	if err != nil {
		_ = uc.Close()
		_ = unix.Close(notifyFD)
		_ = proc.Kill()
		_, _ = a.waitChild(proc)
		return fmt.Errorf("open audit: %w", err)
	}
	approver := a.newApprover(apiURL, token)
	srv := a.newGateServer(policy, approver, auditor, channelID, notifyFD)

	if err := a.sendAck(uc); err != nil {
		_ = uc.Close()
		_ = unix.Close(notifyFD)
		_ = proc.Kill()
		_, _ = a.waitChild(proc)
		return fmt.Errorf("send ack: %w", err)
	}

	ctx, stop := a.notifyContext(context.Background())
	defer stop()

	// Run Server + child.Wait concurrently. Whichever returns first wins:
	//   - child exits (claude quit, or died): close Server, exit with
	//     the child's code
	//   - signal / Server error: kill child, wait, exit
	childExit := make(chan int, 1)
	go func() {
		code, _ := a.waitChild(proc)
		childExit <- code
	}()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Run(ctx)
	}()

	var exitCode int
	var runErr error
	select {
	case code := <-childExit:
		// Child is gone — no more traps will arrive, but srv.Run is
		// likely blocked deep in SECCOMP_IOCTL_NOTIF_RECV which ctx
		// cancellation can't interrupt. Close the transport so the
		// kernel-side filter can drop, cancel ctx for any
		// post-recv work, and don't wait for serverErr: a.exitCode
		// (os.Exit in production) tears the blocked goroutine down.
		// Drain serverErr in a goroutine so tests with a fake
		// exitCode don't leak it.
		_ = srv.Close()
		stop()
		go func() { <-serverErr }()
		_ = uc.Close()
		exitCode = code
	case err := <-serverErr:
		runErr = err
		// Child is still running — SIGTERM it, then reap.
		_ = proc.Signal(syscall.SIGTERM)
		exitCode = <-childExit
		_ = srv.Close()
		_ = uc.Close()
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		fmt.Fprintf(a.stderr, "loop-syscallwrap: gate server: %v\n", runErr)
	}
	// Exit with the child's code so the container's lifecycle mirrors
	// "exec claude" (what we would run without the gate).
	a.exitCode(exitCode)
	return nil
}

// childProcessEnv returns the env the child process should see: the current
// environ with LOOP_SYSCALLWRAP_MODE=child appended (and any caller-provided
// LOOP_SYSCALLWRAP_MODE stripped — we don't want a double-parent loop if a
// wrapper passed it in).
func childProcessEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	prefix := envMode + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			continue
		}
		out = append(out, e)
	}
	out = append(out, envMode+"="+modeChild)
	return out
}

// loadGatePolicy reads a JSON file containing a gateConfigJSON payload and
// returns a compiled *agentgate.Policy. Mirrors the pattern used by
// cmd/loop-dockerproxy — the runner writes this file before spawning us.
func loadGatePolicy(readFile func(string) ([]byte, error), path string) (*agentgate.Policy, error) {
	raw, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg gateConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	decision := types.Decision(cfg.DefaultDecision)
	if decision == "" {
		decision = types.DecisionDeny
	}
	return agentgate.CompilePolicy(decision, cfg.PathRules, cfg.CommandRules, cfg.FileRules)
}

// gateConfigJSON mirrors the subset of internal/config.AgentgateConfig the gate
// server needs. Tags match the snake_case wire format the runner writes.
type gateConfigJSON struct {
	DefaultDecision string              `json:"default_decision"`
	PathRules       []types.PathRule    `json:"path_rules"`
	CommandRules    []types.CommandRule `json:"command_rules"`
	FileRules       []types.FileRule    `json:"file_rules"`
}

// defaultLookupUser resolves HOST_USER into the agent's numeric uid/gid.
// entrypoint.sh already creates this user before we're invoked, so the
// lookup hits /etc/passwd.
func defaultLookupUser(name string) (int, int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	return parseUIDGID(u.Uid, u.Gid)
}

// parseUIDGID converts the string uid/gid fields from os/user into ints.
// Split out from defaultLookupUser so tests can exercise the error paths
// without needing to mint a synthetic /etc/passwd entry.
func parseUIDGID(uidStr, gidStr string) (int, int, error) {
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parse uid %q: %w", uidStr, err)
	}
	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parse gid %q: %w", gidStr, err)
	}
	return uid, gid, nil
}

// defaultSocketpair creates a connected AF_UNIX/SOCK_STREAM pair. Returns
// the parent end and child end as raw fds; callers wrap them into os.File /
// net.UnixConn as needed. On error fds is [0,0] (zero-value from the syscall)
// and the caller must check err before using either fd.
func defaultSocketpair() (int, int, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	return fds[0], fds[1], err
}

// defaultStartChild re-execs /proc/self/exe with the given argv/env, passing
// childEnd as ExtraFiles[0] (which lands at fd 3 in the child) and dropping
// the child to the agent uid/gid via a SysProcAttr.Credential. PDEATHSIG is
// set by the child itself post-install (defaultSetPdeathsig) — setting it
// here would race the thread-lock.
//
// Sentinel uid < 0 or gid < 0 means "inherit current uid/gid" (terminal-exec
// mode, where the caller is already agent-uid and can't setuid).
func defaultStartChild(argv []string, env []string, childEnd *os.File, uid, gid int) (*os.Process, error) {
	return os.StartProcess("/proc/self/exe", argv, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr, childEnd},
		Sys:   childSysProcAttr(uid, gid),
	})
}

// childSysProcAttr builds the SysProcAttr for the re-exec'd child. Factored
// out so tests can assert the Credential-vs-no-Credential decision without
// needing CAP_SETUID to actually call StartProcess.
func childSysProcAttr(uid, gid int) *syscall.SysProcAttr {
	sys := &syscall.SysProcAttr{}
	if uid >= 0 && gid >= 0 {
		sys.Credential = &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		}
	}
	return sys
}

// defaultWaitChild blocks until proc exits and returns the exit code. A
// nonzero code captures any abnormal exit (signal, nonzero-return from
// claude); we propagate it unchanged.
func defaultWaitChild(proc *os.Process) (int, error) {
	state, err := proc.Wait()
	if err != nil {
		return 1, err
	}
	return state.ExitCode(), nil
}

// defaultNotifyContext wires SIGTERM+SIGINT to a cancellable context. The
// returned CancelFunc should be deferred so the signal handler is
// unregistered on normal return.
func defaultNotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT)
}

// defaultSelfExe returns the path the re-exec should launch. Always
// /proc/self/exe — portable across image-layer symlink games.
func defaultSelfExe() string { return "/proc/self/exe" }

// defaultNewApprover builds a production HTTP-backed approver.
func defaultNewApprover(apiURL, token string) agentgate.Approver {
	return httpapprover.New(apiURL, token, nil, nil)
}

// defaultNewGateServer builds a production *agentgate.Server wrapped in
// the gateServer interface so runParent can receive it uniformly.
func defaultNewGateServer(policy *agentgate.Policy, approver agentgate.Approver, auditor agentgate.Auditor, channelID string, notifyFD int) gateServer {
	return agentgate.NewServer(policy, approver, auditor, channelID, notifyFD)
}

// defaultOpenAuditor resolves the audit sink at parent startup. Empty dir →
// silent gate (NopAuditor); non-empty dir → FileAuditor writing rotating
// jsonl under dir. A directory that doesn't exist / can't be written is a
// hard error: we'd rather fail container startup than run un-audited.
func defaultOpenAuditor(dir string, retentionDays int, verbose bool) (agentgate.Auditor, error) {
	if dir == "" {
		return agentgate.NopAuditor{}, nil
	}
	return agentgate.NewFileAuditor(dir, retentionDays, verbose)
}

// parseAuditRetention parses LOOP_GATE_AUDIT_RETENTION_DAYS. Empty /
// non-numeric / negative → 0 (FileAuditor treats 0 as "no pruning").
// Intentionally permissive: a malformed value shouldn't fail the gate
// startup, since missing retention just means older files stick around.
func parseAuditRetention(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
