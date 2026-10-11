package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin/gatebin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

const (
	// pluginIdleTimeout is how long a plugin container may go unused before
	// it's stopped.
	pluginIdleTimeout = 15 * time.Minute
	// pluginStartWait caps how long a started plugin container has to answer
	// its health check.
	pluginStartWait = 30 * time.Second
	// pluginIdleInterval is how often plugin containers left idle, or in a
	// project that stopped enabling their plugin, are looked for.
	pluginIdleInterval = time.Minute
)

// pluginDockerClient is the Docker API plugins are installed and run with.
type pluginDockerClient interface {
	runtime.DockerClient
	install.DockerClient
	ServerVersion(ctx context.Context) (types.Version, error)
	Close() error
}

// pluginStore keeps installed plugins and their containers.
type pluginStore interface {
	runtime.Store
	install.ServiceStore
}

// plugins is the plugin system the daemon runs when plugins.enabled is set.
type plugins struct {
	docker  pluginDockerClient
	runtime *runtime.Manager
	service *install.Service
}

// stop stops every plugin container, at shutdown.
func (p *plugins) stop(ctx context.Context) {
	p.runtime.StopAll(ctx)
	_ = p.docker.Close()
}

// newPlugins builds the plugin system, or returns nil when plugins are
// disabled. It removes the plugin containers an earlier daemon left behind.
func (a *app) newPlugins(ctx context.Context, cfg *config.Config, reload func() (*config.Config, error), store db.Store, tokens *apiauth.Registry, gate container.ImageGate, logger *slog.Logger) (*plugins, error) {
	if !cfg.Plugins.Enabled {
		return nil, nil
	}
	ps, ok := store.(pluginStore)
	if !ok {
		return nil, errors.New("plugins need the SQLite store")
	}
	cli, err := a.newPluginDocker()
	if err != nil {
		return nil, fmt.Errorf("creating the plugin docker client: %w", err)
	}
	root := filepath.Join(cfg.LoopDir, "plugins")
	mgr := runtime.NewManager(cli, ps, tokens, runtime.Config{
		Root:        root,
		APIURL:      container.AgentAPIBase(cfg),
		IdleTimeout: pluginIdleTimeout,
		StartWait:   pluginStartWait,
		Proxy:       container.ProxySettingsFromConfig(cfg),
		Gate:        a.pluginGate(cfg, cli),
	}, logger)
	mgr.SetImageGate(gate)
	if err := mgr.RemoveLeftovers(ctx); err != nil {
		logger.Warn("removing leftover plugin containers", "error", err)
	}
	installer := install.NewInstaller(cli, ps, root, a.version)
	return &plugins{docker: cli, runtime: mgr, service: install.NewService(installer, mgr, ps, proxy.NewClient(&http.Client{}), pluginEnabled(cfg, reload, store))}, nil
}

// pluginActionRunner runs the scheduled tasks of plugin actions on svc.
func pluginActionRunner(svc *install.Service) orchestrator.PluginActionRunner {
	return func(ctx context.Context, pluginID, action, channelID, dirPath string, taskID int64) error {
		return svc.RunAction(ctx, pluginID, action, runtime.Channel{ID: channelID, DirPath: dirPath}, proxy.ActionRequest{Trigger: proxy.TriggerSchedule, TaskID: taskID})
	}
}

// pluginEnabled reports whether a plugin is enabled in a channel's project:
// the project config's plugins.<id>.enabled, else the global one. A
// worktree's project config is its checkout's, as the config API has it; a
// channel without a dir has its project config where the API keeps it.
func pluginEnabled(cfg *config.Config, reload func() (*config.Config, error), store db.ChannelGetter) install.Enabled {
	return func(ctx context.Context, pluginID string, ch runtime.Channel) (bool, error) {
		dir := ch.DirPath
		c, err := store.GetChannel(ctx, ch.ID)
		if err != nil {
			return false, err
		}
		if c != nil {
			dir = cmp.Or(db.WorktreeRootDirPath(ctx, store, c), dir)
		}
		if dir == "" {
			dir = filepath.Join(cfg.LoopDir, ch.ID, "work")
		}
		merged, err := mergedProjectConfig(cfg, reload, dir, "")
		if err != nil {
			return false, err
		}
		return merged.Plugins.EnabledFor(pluginID), nil
	}
}

// pluginGate is how plugin containers run under agentgate, or nil when
// it's off. The gate's loop binary is found on the first start.
func (a *app) pluginGate(cfg *config.Config, cli pluginDockerClient) *runtime.GateConfig {
	if !cfg.Gates.Agentgate.Enabled {
		return nil
	}
	bin := gatebin.New(gatebin.Config{
		Override: cfg.Plugins.GateBinary,
		Version:  a.version,
		CacheDir: filepath.Join(cfg.LoopDir, "cache", "gate-bin"),
		Arch: func(ctx context.Context) (string, error) {
			v, err := cli.ServerVersion(ctx)
			return v.Arch, err
		},
		Get: gatebin.HTTPGet(&http.Client{Timeout: 5 * time.Minute}),
	})
	return &runtime.GateConfig{
		Binary:             bin.Path,
		AuditRetentionDays: cfg.Gates.Audit.RetentionDays,
		AuditVerbose:       cfg.Gates.Audit.Verbose,
	}
}
