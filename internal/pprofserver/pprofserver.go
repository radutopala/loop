// Package pprofserver serves the daemon's Go pprof profiles on a listener of
// their own, started and stopped as the pprof config block changes.
package pprofserver

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/radutopala/loop/internal/config"
)

// Runner keeps the pprof listener in step with the config: listening on
// pprof.addr while pprof.enabled is on, closed otherwise.
type Runner struct {
	Config func() (*config.Config, error)
	Listen func(network, addr string) (net.Listener, error)
	Logger *slog.Logger

	srv *http.Server
	// addr is what srv listens on; failed is the address that last failed
	// to listen, so a lasting failure is logged once.
	addr   string
	failed string
}

// New returns a Runner reading the config with reload.
func New(reload func() (*config.Config, error), logger *slog.Logger) *Runner {
	return &Runner{Config: reload, Listen: net.Listen, Logger: logger}
}

// Run syncs the listener with the config every tick until ctx is done, then
// closes it.
func (r *Runner) Run(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		r.sync()
		select {
		case <-ctx.Done():
			r.stop()
			return
		case <-ticker.C:
		}
	}
}

// sync starts, moves or stops the listener to match the config. A config
// that fails to load leaves it as it is.
func (r *Runner) sync() {
	cfg, err := r.Config()
	if err != nil {
		return
	}
	want := ""
	if cfg.Pprof.Enabled {
		want = cfg.Pprof.Addr
	}
	if want == "" {
		r.failed = ""
	}
	if want == r.addr {
		return
	}
	r.stop()
	if want == "" {
		return
	}
	ln, err := r.Listen("tcp", want)
	if err != nil {
		if r.failed != want {
			r.Logger.Warn("pprof listen", "addr", want, "error", err)
		}
		r.failed = want
		return
	}
	r.failed = ""
	srv := &http.Server{Handler: Handler(), ReadHeaderTimeout: 10 * time.Second}
	r.srv, r.addr = srv, want
	// A Close before Serve starts still closes ln: Serve then returns at once.
	go func() { _ = srv.Serve(ln) }()
	r.Logger.Info("pprof listening", "addr", ln.Addr().String())
}

// stop closes the listener, if it's open.
func (r *Runner) stop() {
	if r.srv == nil {
		return
	}
	_ = r.srv.Close()
	r.Logger.Info("pprof stopped", "addr", r.addr)
	r.srv = nil
	r.addr = ""
}

// Handler serves the pprof index and profiles under /debug/pprof/.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
