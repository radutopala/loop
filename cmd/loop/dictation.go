package main

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/dictation"
	"github.com/radutopala/loop/internal/localserver"
)

// dictationStartupTimeout bounds whisper-server's start, which loads the
// model: several seconds on a busy machine.
const dictationStartupTimeout = time.Minute

// dictationAutoDownloadInterval is how often the daemon checks whether
// dictation was turned on and its model needs fetching.
const dictationAutoDownloadInterval = 30 * time.Second

// newDictation returns the dictation service and the whisper-server it runs
// on the host. Models download through download (the proxy-aware transport);
// whisper-server is reached on loopback without a proxy. The model, language
// and threads follow reload, falling back to cfg when it fails.
func newDictation(ctx context.Context, cfg *config.Config, reload func() (*config.Config, error), download http.RoundTripper, logger *slog.Logger) (*dictation.Service, *localserver.Server) {
	local := &http.Client{Transport: &http.Transport{Proxy: nil}}
	args := &dictationArgs{}
	server := localserver.New(localserver.Config{
		Binary:         localserver.ExeName("whisper-server", runtime.GOOS),
		Args:           args.args,
		HealthPath:     "/health",
		PidFile:        filepath.Join(cfg.LoopDir, "whisper-server.pid"),
		IdleStop:       time.Duration(cfg.Dictation.IdleStopSec) * time.Second,
		StartupTimeout: dictationStartupTimeout,
	}, localserver.RealSystem{}, local, logger)
	downloader := dictation.NewDownloader(filepath.Join(cfg.LoopDir, "models"), &http.Client{Transport: download}, logger)
	current := func() dictation.Config {
		c, err := reload()
		if err != nil {
			c = cfg
		}
		return dictation.Config{Enabled: c.Dictation.Enabled, Model: c.Dictation.Model, Language: c.Dictation.Language, Threads: c.Dictation.Threads}
	}
	args.svc = dictation.NewService(ctx, current, server, downloader, dictation.NewEngine(local))
	return args.svc, server
}

// dictationArgs gives whisper-server the arguments of the service it is
// created before.
type dictationArgs struct {
	svc *dictation.Service
}

func (d *dictationArgs) args(port int) []string { return d.svc.ServerArgs(port) }
