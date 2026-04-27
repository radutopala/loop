//go:build linux

package syscallwrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/agentgate"
)

const (
	// envMode selects parent vs child path. Unset/anything-else → parent. The
	// parent sets LOOP_SYSCALLWRAP_MODE=child in the env it hands to the
	// re-execed self, so the child branch runs only in the spawned process.
	envMode = "LOOP_SYSCALLWRAP_MODE"

	// envChannelID is the bot-channel id the gate-server prompts on. Used by
	// both child (sent over the handshake) and parent (stamped on Server).
	envChannelID = "LOOP_CHANNEL_ID"

	// Parent-only env vars. Missing values are a hard error in the parent
	// (fail-closed: without a policy or approver we can't mediate safely).
	envPolicyFile = "LOOP_GATE_POLICY_FILE"
	envAPIURL     = "API_URL"
	envToken      = "LOOP_GATE_TOKEN"
	envHostUser   = "HOST_USER"

	// Audit env vars. All optional — unset envAuditDir means NopAuditor
	// (silent gate). envAuditDir is the (bind-mounted) directory to write
	// rotating agentgate-YYYY-MM-DD.jsonl files into. envAuditRetentionDays
	// is an integer; empty / 0 / negative disables pruning. envAuditVerbose
	// set to "1" flips the file auditor into verbose mode (log silent
	// allows too); anything else keeps the default focused trail of denies
	// plus user-clicked decisions.
	envAuditDir           = "LOOP_GATE_AUDIT_DIR"
	envAuditRetentionDays = "LOOP_GATE_AUDIT_RETENTION_DAYS"
	envAuditVerbose       = "LOOP_GATE_AUDIT_VERBOSE"

	modeChild = "child"

	// childHandshakeFD is the fd the parent assigns to the child-end of the
	// socketpair via os.ProcAttr.ExtraFiles. ExtraFiles[0] starts at fd 3
	// because 0/1/2 are stdio.
	childHandshakeFD = 3
)

// gateServer is the subset of *agentgate.Server the parent drives. Exposed as
// an interface so runParent is unit-testable with a stub. Close releases the
// notify transport on shutdown — needed because Run can wedge in a kernel
// ioctl that ctx cancellation alone won't interrupt.
type gateServer interface {
	Run(ctx context.Context) error
	Close() error
}

// app holds injectable dependencies. Production wiring in newApp points each
// field at the real syscall / runtime helper; tests swap in fakes. Splitting
// fields by mode (child vs parent) keeps each unit test focused on one path.
type app struct {
	// Shared
	getenv func(string) string
	// args is what parseArgs consumes: sentinel [0] + forwarded arguments
	// ([--] <target> [args...]). The subcommand wrapper prepends a sentinel
	// so parseArgs, which expects argv[0] to be a program name, works
	// unchanged. For ad-hoc callers that re-exec /proc/self/exe themselves
	// this can be os.Args directly.
	args []string
	// selfArgv is the full argv to re-exec /proc/self/exe with (i.e. os.Args
	// for the outer `loop` binary, which re-dispatches to this subcommand in
	// the child). Distinct from args because the child must see the
	// subcommand boundary (`loop syscallwrap -- ...`), whereas parseArgs
	// only cares about the target-command tail.
	selfArgv []string
	environ  func() []string

	// Child mode
	lockOSThread func()
	setPdeathsig func(sig syscall.Signal) error
	parentConn   func() (*net.UnixConn, error)
	install      func() (int, error)
	send         func(conn *net.UnixConn, channelID string, notifyFD int) error
	readAck      func(conn *net.UnixConn) error
	closeFD      func(fd int) error
	lookPath     func(name string) (string, error)
	exec         func(argv0 string, argv []string, envv []string) error

	// Parent mode
	readFile      func(path string) ([]byte, error)
	lookupUser    func(name string) (uid, gid int, err error)
	getuid        func() int
	socketpair    func() (parentFD, childFD int, err error)
	startChild    func(argv []string, env []string, childEnd *os.File, uid, gid int) (*os.Process, error)
	newApprover   func(apiURL, token string) agentgate.Approver
	newGateServer func(policy *agentgate.Policy, approver agentgate.Approver, auditor agentgate.Auditor, channelID string, notifyFD int) gateServer
	// openAuditor returns a live Auditor for the given directory +
	// retention. dir=="" → NopAuditor (no-op). Errors propagate so the
	// parent fails fast rather than silently running un-audited; the dir
	// is supposed to be bind-mounted by the runner, so an error here is
	// "operator misconfiguration", not "agent attack".
	openAuditor   func(dir string, retentionDays int, verbose bool) (agentgate.Auditor, error)
	receiveHS     func(conn *net.UnixConn) (channelID string, notifyFD int, err error)
	sendAck       func(conn *net.UnixConn) error
	waitChild     func(p *os.Process) (int, error)
	notifyContext func(parent context.Context) (context.Context, context.CancelFunc)
	selfExe       func() string
	exitCode      func(code int)
	stderr        io.Writer
}

// newApp wires production defaults. args / selfArgv are caller-provided (the
// subcommand wrapper in cmd/loop passes them) since this package no longer
// owns a main() of its own.
func newApp() *app {
	fi := agentgate.NewFilterInstaller()
	return &app{
		getenv:        os.Getenv,
		environ:       os.Environ,
		lockOSThread:  runtime.LockOSThread,
		setPdeathsig:  defaultSetPdeathsig,
		parentConn:    defaultParentConn,
		install:       fi.Install,
		send:          sendHandshake,
		readAck:       readAck,
		closeFD:       unix.Close,
		lookPath:      exec.LookPath,
		exec:          syscall.Exec,
		readFile:      os.ReadFile,
		lookupUser:    defaultLookupUser,
		getuid:        os.Getuid,
		socketpair:    defaultSocketpair,
		startChild:    defaultStartChild,
		newApprover:   defaultNewApprover,
		newGateServer: defaultNewGateServer,
		openAuditor:   defaultOpenAuditor,
		receiveHS:     agentgate.ReceiveHandshake,
		sendAck:       agentgate.SendAck,
		waitChild:     defaultWaitChild,
		notifyContext: defaultNotifyContext,
		selfExe:       defaultSelfExe,
		exitCode:      os.Exit,
		stderr:        os.Stderr,
	}
}

// runMain is the testable main body. main_linux.go's main() does nothing but
// wrap this and call os.Exit. Any top-level error is written to errW.
func runMain(errW io.Writer, a *app) int {
	if err := a.run(); err != nil {
		fmt.Fprintf(errW, "loop-syscallwrap: %v\n", err)
		return 1
	}
	return 0
}

// run dispatches to the parent or child path based on LOOP_SYSCALLWRAP_MODE.
// The parent branch runs when entrypoint.sh exec's loop-syscallwrap; the
// child branch runs when the parent re-execs /proc/self/exe with the env var
// set to "child".
func (a *app) run() error {
	if a.getenv(envMode) == modeChild {
		return a.runChild()
	}
	return a.runParent()
}

// Run is the exported entry point for the `loop syscallwrap` subcommand. It
// builds the production app, plumbs in the subcommand-forwarded args and the
// outer binary's os.Args (used to re-exec /proc/self/exe in the child), and
// returns the exit code the caller should os.Exit with.
//
// forwardArgs is the tail cobra passed to the subcommand ([--] <target>
// [args...]). We prepend a sentinel so parseArgs' expectation that argv[0]
// is the program name survives unchanged. selfArgv is the outer argv
// (os.Args for `loop syscallwrap ...`), used when the parent re-execs
// /proc/self/exe so cobra re-dispatches to this subcommand in the child.
func Run(stderr io.Writer, forwardArgs, selfArgv []string) int {
	a := newApp()
	a.args = append([]string{"loop-syscallwrap"}, forwardArgs...)
	a.selfArgv = selfArgv
	return runMain(stderr, a)
}

// parseArgs returns everything after the wrapper's own argv[0] and an
// optional "--" separator. entrypoint.sh invokes us as
//
//	loop syscallwrap -- <cmd> [args...]
//
// but the "--" is tolerated-absent so ad-hoc invocations
// (`loop syscallwrap claude -p hi`) also work.
func parseArgs(args []string) ([]string, error) {
	if len(args) < 2 {
		return nil, errors.New("usage: loop syscallwrap [--] <cmd> [args...]")
	}
	rest := args[1:]
	if rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return nil, errors.New("no target command after --")
	}
	return rest, nil
}
