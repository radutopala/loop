package dockerproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/httpapprover"
)

const (
	envSocket     = "LOOP_DOCKERPROXY_SOCKET"
	envPolicyFile = "LOOP_DOCKERPROXY_POLICY_FILE"
	envUpstream   = "LOOP_DOCKERPROXY_UPSTREAM"
	envAPIURL     = "API_URL"
	envToken      = "LOOP_GATE_TOKEN"
	envCID        = "LOOP_CONTAINER_ID"
	envChannelID  = "LOOP_CHANNEL_ID"
	envNestedDir  = "LOOP_DOCKERPROXY_NESTED_DIR"
	envReadOnly   = "LOOP_DOCKERPROXY_READONLY_DIRS"
	envBindRoots  = "LOOP_DOCKERPROXY_BIND_ROOTS"

	defaultSocket   = "/var/run/docker.sock"
	defaultUpstream = "/var/run/docker.sock.host"
)

// app bundles injectable dependencies so runMain is unit-testable without
// touching the filesystem, the network, or signals.
type app struct {
	getenv     func(string) string
	readFile   func(path string) ([]byte, error)
	removeAll  func(path string) error
	listenUnix func(path string) (net.Listener, error)
	chmod      func(path string, mode os.FileMode) error

	// serve runs the HTTP server on ln and blocks until Shutdown or fatal error.
	// Injection-friendly so tests can verify the handler without dialing sockets.
	serve func(ctx context.Context, ln net.Listener, handler http.Handler) error

	// notifyContext wires SIGTERM/SIGINT into a cancellable context. Tests pass
	// a pre-cancelled context to verify the graceful-shutdown path.
	notifyContext func(parent context.Context) (context.Context, context.CancelFunc)

	// newApprover builds the Approver. Exposed so tests can substitute a stub.
	newApprover func(apiURL, token string) Approver

	// evalSymlinks resolves a path's symlink chain. Stamped onto every
	// source_path_in body-rule check so an agent cannot bypass a Bind-source
	// deny rule by symlinking a workspace path to the target. Tests can
	// substitute a fake resolver to drive the deny-on-resolve-failure paths.
	evalSymlinks SymlinkResolver

	// lookupVolume names the volume the container cid mounts at dir, asking
	// the daemon over upstream.
	lookupVolume func(ctx context.Context, upstream, cid, dir string) (string, error)
}

// newApp wires production defaults.
func newApp() *app {
	return &app{
		getenv:        os.Getenv,
		readFile:      os.ReadFile,
		removeAll:     os.Remove,
		listenUnix:    defaultListenUnix,
		chmod:         os.Chmod,
		serve:         defaultServe,
		notifyContext: defaultNotifyContext,
		newApprover:   defaultNewApprover,
		evalSymlinks:  filepath.EvalSymlinks,
		lookupVolume:  lookupNestedVolume,
	}
}

// Run is the exported entry point for the `loop dockerproxy` subcommand.
// Returns the exit code the caller should os.Exit with. Any setup error is
// printed to stderr; stdout is reserved for structured log events.
func Run(stdout, stderr io.Writer) int {
	return runMain(stdout, stderr, newApp())
}

// runMain is the testable entry point. Run() wraps this and returns the exit
// code. Any setup error is printed to errW; stdout (outW) is reserved for
// structured log events.
func runMain(outW, errW io.Writer, a *app) int {
	if err := a.run(outW); err != nil {
		fmt.Fprintf(errW, "loop-dockerproxy: %v\n", err)
		return 1
	}
	return 0
}

func (a *app) run(outW io.Writer) error {
	logger := slog.New(slog.NewTextHandler(outW, &slog.HandlerOptions{Level: slog.LevelInfo}))

	socketPath := a.getenv(envSocket)
	if socketPath == "" {
		socketPath = defaultSocket
	}
	policyFile := a.getenv(envPolicyFile)
	if policyFile == "" {
		return errors.New(envPolicyFile + " is required")
	}
	upstream := a.getenv(envUpstream)
	if upstream == "" {
		upstream = defaultUpstream
	}
	apiURL := a.getenv(envAPIURL)
	if apiURL == "" {
		return errors.New(envAPIURL + " is required")
	}
	token := a.getenv(envToken)
	if token == "" {
		return errors.New(envToken + " is required")
	}
	cid := a.getenv(envCID)
	channelID := a.getenv(envChannelID)

	raw, err := a.readFile(policyFile)
	if err != nil {
		return fmt.Errorf("read policy %s: %w", policyFile, err)
	}
	var cfg config.DockerProxyConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse policy: %w", err)
	}
	policy, err := CompilePolicy(cfg.DefaultDecision, cfg.HTTPRules, cfg.BodyRules)
	if err != nil {
		return fmt.Errorf("compile policy: %w", err)
	}
	policy.SetSymlinkResolver(a.evalSymlinks)

	approver := a.newApprover(apiURL, token)

	ctx, stop := a.notifyContext(context.Background())
	defer stop()

	// The nested socket is best-effort: without it the proxy still serves
	// the agent and rejects docker socket mounts (NestedVolume stays empty).
	var nestedLn net.Listener
	var nestedVolume string
	if dir := a.getenv(envNestedDir); dir != "" {
		nestedLn, nestedVolume, err = a.listenNested(ctx, upstream, cid, dir)
		if err != nil {
			logger.Warn("nested docker socket unavailable; socket mounts will be rejected", "error", err)
		}
	}

	srv, err := NewServer(ServerConfig{
		CID:          cid,
		ChannelID:    channelID,
		Policy:       policy,
		Approver:     approver,
		DockerSock:   upstream,
		NestedVolume: nestedVolume,
		EvalSymlinks: a.evalSymlinks,
		ReadOnlyDirs: dirList(a.getenv(envReadOnly)),
		BindRoots:    dirList(a.getenv(envBindRoots)),
	})
	if err != nil {
		closeListener(nestedLn)
		return fmt.Errorf("build server: %w", err)
	}

	ln, err := a.listenSocket(socketPath)
	if err != nil {
		closeListener(nestedLn)
		return err
	}

	logger.Info("loop-dockerproxy started",
		"socket", socketPath,
		"upstream", upstream,
		"policy_file", policyFile,
		"cid", cid,
		"nested_volume", nestedVolume,
	)

	if nestedLn != nil {
		go func() {
			if err := a.serve(ctx, nestedLn, srv); err != nil {
				logger.Warn("nested docker socket stopped", "error", err)
			}
		}()
	}
	return a.serve(ctx, ln, srv)
}

// dirList parses a colon-separated directory list (LOOP_DOCKERPROXY_READONLY_DIRS,
// LOOP_DOCKERPROXY_BIND_ROOTS), keeping absolute paths only.
func dirList(v string) []string {
	var dirs []string
	for d := range strings.SplitSeq(v, ":") {
		if strings.HasPrefix(d, "/") {
			dirs = append(dirs, filepath.Clean(d))
		}
	}
	return dirs
}

// listenSocket replaces any stale socket at path (e.g. from a restart
// inside the same tmpfs) and listens on it.
func (a *app) listenSocket(path string) (net.Listener, error) {
	// Ignore removal errors — bind will fail loudly if the path is busy.
	_ = a.removeAll(path)
	ln, err := a.listenUnix(path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}
	// 0o666 so the non-root agent user (and the users of containers it
	// starts) can dial the proxy without group membership plumbing. The
	// attack surface is the container itself, not filesystem perms.
	if err := a.chmod(path, 0o666); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return ln, nil
}

// listenNested listens on the nested socket in dir and names the volume
// mounted there, which container creates mount in place of the docker
// socket.
func (a *app) listenNested(ctx context.Context, upstream, cid, dir string) (net.Listener, string, error) {
	volume, err := a.lookupVolume(ctx, upstream, cid, dir)
	if err != nil {
		return nil, "", err
	}
	ln, err := a.listenSocket(filepath.Join(dir, nestedSocketName))
	if err != nil {
		return nil, "", err
	}
	return ln, volume, nil
}

// closeListener closes ln when set.
func closeListener(ln net.Listener) {
	if ln != nil {
		_ = ln.Close()
	}
}

// defaultListenUnix opens a SOCK_STREAM unix listener.
func defaultListenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

// defaultServe runs an http.Server and blocks until ctx is done, then calls
// Shutdown with a bounded timeout. Returns nil on clean shutdown.
//
// ConnContext is wired to connContextPeerPID so unix-domain peers'
// SO_PEERCRED-derived PIDs are stamped on each request's context. The
// dockerproxy Server reads them via peerPIDFromContext to attribute
// approval prompts back to the originating process tree (chat vs. terminal).
func defaultServe(ctx context.Context, ln net.Listener, handler http.Handler) error {
	httpd := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext:       connContextPeerPID,
	}
	errCh := make(chan error, 1)
	go func() {
		err := httpd.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpd.Shutdown(shutCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// defaultNotifyContext wires SIGTERM+SIGINT to a cancellable context.
func defaultNotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT)
}

// defaultNewApprover builds a production Approver.
func defaultNewApprover(apiURL, token string) Approver {
	return httpapprover.New(apiURL, token, nil, nil)
}
