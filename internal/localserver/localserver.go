// Package localserver runs a native helper HTTP server, such as
// whisper-server, as a child of the daemon: it finds the binary, starts it
// on a free loopback port the first time it's needed, waits until it
// answers, and stops it after it has been idle for a while.
package localserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrBinaryMissing is returned when the server binary is neither next to the
// running executable nor on PATH.
var ErrBinaryMissing = errors.New("binary not found")

// Process is a started server process.
type Process interface {
	Pid() int
	Wait() error
	Kill() error
}

// HTTPClient abstracts HTTP calls for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// System is the OS surface the server needs.
type System interface {
	Executable() (string, error)
	Stat(name string) (fs.FileInfo, error)
	LookPath(file string) (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	// FindProcess returns the running process pid, or an error when it
	// isn't running.
	FindProcess(pid int) (Process, error)
	// Start launches name with args, writing its output to out.
	Start(name string, args []string, out io.Writer) (Process, error)
	// FreePort returns a loopback TCP port nothing listens on.
	FreePort() (int, error)
}

// Config describes the server to run.
type Config struct {
	// Binary is the executable's file name, e.g. "whisper-server". On
	// Windows ".exe" is added (see ExeName).
	Binary string
	// Args returns the arguments to start it with, given the port it must
	// listen on (on 127.0.0.1).
	Args func(port int) []string
	// HealthPath is requested until it answers 200 to tell the server is up.
	HealthPath string
	// PidFile records the running server, so a daemon that crashed
	// leaves no server running past the next start.
	PidFile string
	// IdleStop stops the server once it has gone this long unused.
	IdleStop time.Duration
	// StartupTimeout bounds the wait for the server to answer.
	StartupTimeout time.Duration
}

// Server is a lazily started local server. The zero value is not usable; use
// New.
type Server struct {
	cfg       Config
	sys       System
	http      HTTPClient
	logger    *slog.Logger
	now       func() time.Time
	pollDelay time.Duration

	mu       sync.Mutex
	proc     Process
	port     int
	exited   chan struct{}
	lastUsed time.Time
	busy     int
	output   *tailBuffer
}

// New returns a server for cfg. It starts nothing.
func New(cfg Config, sys System, client HTTPClient, logger *slog.Logger) *Server {
	return &Server{
		cfg:       cfg,
		sys:       sys,
		http:      client,
		logger:    logger,
		now:       time.Now,
		pollDelay: 100 * time.Millisecond,
	}
}

// ExeName adds the executable suffix goos uses to name.
func ExeName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// FindBinary returns the server binary: the one next to the running
// executable (where the app bundle ships it), else the one on PATH.
func (s *Server) FindBinary() (string, error) {
	if exe, err := s.sys.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		bundled := filepath.Join(filepath.Dir(exe), s.cfg.Binary)
		if info, err := s.sys.Stat(bundled); err == nil && !info.IsDir() {
			return bundled, nil
		}
	}
	if p, err := s.sys.LookPath(s.cfg.Binary); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s: %w", s.cfg.Binary, ErrBinaryMissing)
}

// Running reports whether the server process is up.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive()
}

// alive reports whether the started process is still running. Callers hold mu.
func (s *Server) alive() bool {
	if s.proc == nil {
		return false
	}
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// Acquire starts the server if it isn't running and returns its base URL.
// Every successful Acquire must be paired with a Release; the server isn't
// stopped for idleness in between.
func (s *Server) Acquire(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.alive() {
		if err := s.start(ctx); err != nil {
			return "", err
		}
	}
	s.busy++
	s.lastUsed = s.now()
	return s.baseURL(), nil
}

// Release marks the end of a use begun by Acquire.
func (s *Server) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = max(s.busy-1, 0)
	s.lastUsed = s.now()
}

func (s *Server) baseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(s.port)
}

// start launches the server and waits until it answers. Callers hold mu.
func (s *Server) start(ctx context.Context) error {
	bin, err := s.FindBinary()
	if err != nil {
		return err
	}
	s.reapStale(ctx)
	port, err := s.sys.FreePort()
	if err != nil {
		return fmt.Errorf("free port: %w", err)
	}
	out := &tailBuffer{max: 4096}
	proc, err := s.sys.Start(bin, s.cfg.Args(port), out)
	if err != nil {
		return fmt.Errorf("start %s: %w", s.cfg.Binary, err)
	}
	exited := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(exited)
	}()
	s.proc, s.port, s.exited, s.output = proc, port, exited, out
	s.logger.Info("local server starting", "binary", bin, "port", port, "pid", proc.Pid())
	if err := s.waitReady(ctx, exited); err != nil {
		_ = proc.Kill()
		<-exited
		s.proc = nil
		return fmt.Errorf("%s: %w: %s", s.cfg.Binary, err, strings.TrimSpace(out.String()))
	}
	_ = s.sys.WriteFile(s.cfg.PidFile, []byte(strconv.Itoa(proc.Pid())+" "+strconv.Itoa(port)), 0o600)
	return nil
}

// errExited reports that the server quit before it answered.
var errExited = errors.New("exited during startup")

func (s *Server) waitReady(ctx context.Context, exited <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.StartupTimeout)
	defer cancel()
	for {
		if s.healthy(ctx, s.port) {
			return nil
		}
		select {
		case <-exited:
			return errExited
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.pollDelay):
		}
	}
}

func (s *Server) healthy(ctx context.Context, port int) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+s.cfg.HealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// reapStale kills a server a previous daemon left running, recorded in the
// pid file. It is killed only while it still answers on its recorded port,
// so a pid reused by another process is left alone.
func (s *Server) reapStale(ctx context.Context) {
	data, err := s.sys.ReadFile(s.cfg.PidFile)
	if err != nil {
		return
	}
	_ = s.sys.Remove(s.cfg.PidFile)
	var pid, port int
	if _, err := fmt.Sscanf(string(data), "%d %d", &pid, &port); err != nil {
		return
	}
	proc, err := s.sys.FindProcess(pid)
	if err != nil {
		return
	}
	hctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !s.healthy(hctx, port) {
		return
	}
	s.logger.Info("stopping a local server left running", "binary", s.cfg.Binary, "pid", pid)
	_ = proc.Kill()
}

// Stop kills the server if it's running.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop()
}

func (s *Server) stop() {
	if !s.alive() {
		return
	}
	_ = s.proc.Kill()
	<-s.exited
	s.proc = nil
	_ = s.sys.Remove(s.cfg.PidFile)
}

// StopIfIdle stops the server when nothing uses it and it has been unused
// for IdleStop. It reports whether it stopped it.
func (s *Server) StopIfIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.alive() || s.busy > 0 || s.now().Sub(s.lastUsed) < s.cfg.IdleStop {
		return false
	}
	s.logger.Info("stopping idle local server", "binary", s.cfg.Binary)
	s.stop()
	return true
}

// RunIdleMonitor calls StopIfIdle every interval until ctx ends, then stops
// the server.
func (s *Server) RunIdleMonitor(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return
		case <-ticker.C:
			s.StopIfIdle()
		}
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// RealSystem is System backed by the OS. Listen, when set, replaces
// net.Listen in FreePort.
type RealSystem struct {
	Listen func(network, address string) (net.Listener, error)
}

func (RealSystem) Executable() (string, error)           { return os.Executable() }
func (RealSystem) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }
func (RealSystem) LookPath(file string) (string, error)  { return exec.LookPath(file) }
func (RealSystem) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (RealSystem) Remove(name string) error              { return os.Remove(name) }
func (RealSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// FindProcess returns pid when a signal-0 probe (on Unix) says it runs.
func (RealSystem) FindProcess(pid int) (Process, error) {
	p, err := os.FindProcess(pid)
	if err == nil {
		err = probe(p)
	}
	if err != nil {
		return nil, err
	}
	return osProcess{p: p}, nil
}

func (RealSystem) Start(name string, args []string, out io.Writer) (Process, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = out, out
	// A child the server spawned may keep the output pipe open after the
	// server dies; don't let Wait block on it.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmdProcess{cmd: cmd}, nil
}

func (r RealSystem) FreePort() (int, error) {
	listen := r.Listen
	if listen == nil {
		listen = net.Listen
	}
	l, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type cmdProcess struct{ cmd *exec.Cmd }

func (p cmdProcess) Pid() int    { return p.cmd.Process.Pid }
func (p cmdProcess) Wait() error { return p.cmd.Wait() }
func (p cmdProcess) Kill() error { return p.cmd.Process.Kill() }

type osProcess struct{ p *os.Process }

func (p osProcess) Pid() int { return p.p.Pid }
func (p osProcess) Wait() error {
	_, err := p.p.Wait()
	return err
}
func (p osProcess) Kill() error { return p.p.Kill() }
