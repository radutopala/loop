package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin/gatebin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/workflow"
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

// pluginStore keeps installed plugins, their containers and their agent
// runs.
type pluginStore interface {
	runtime.Store
	install.ServiceStore
	orchestrator.PluginRunStore
	api.PluginBadgeStore
	api.PluginSwitchStore
}

// plugins is the plugin system the daemon runs when plugins.enabled is set.
type plugins struct {
	docker  pluginDockerClient
	store   pluginStore
	runtime *runtime.Manager
	service *install.Service
}

// stop stops every plugin container, at shutdown.
func (p *plugins) stop(ctx context.Context) {
	p.runtime.StopAll(ctx)
	_ = p.docker.Close()
}

// newPlugins builds the plugin system, or returns nil when plugins are
// disabled. It removes the plugin containers an earlier daemon left behind,
// and those of channels deleted since.
func (a *app) newPlugins(ctx context.Context, cfg *config.Config, reload func() (*config.Config, error), store db.Store, tokens *apiauth.Registry, gate container.ImageGate, registry runtime.Registry, logger *slog.Logger) (*plugins, error) {
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
	mgr.SetRegistry(registry)
	installer := install.NewInstaller(cli, ps, root, a.version)
	svc := install.NewService(installer, mgr, ps, proxy.NewClient(&http.Client{}), pluginProject(cfg, reload, store))
	if err := errors.Join(mgr.RemoveLeftovers(ctx), svc.PruneChannels(ctx)); err != nil {
		logger.Warn("removing leftover plugin containers", "error", err)
	}
	return &plugins{docker: cli, store: ps, runtime: mgr, service: svc}, nil
}

// pluginActionRunner runs the scheduled tasks of plugin actions on svc.
func pluginActionRunner(svc *install.Service) orchestrator.PluginActionRunner {
	return func(ctx context.Context, pluginID, action, channelID, dirPath string, taskID int64) error {
		return svc.RunAction(ctx, pluginID, action, runtime.Channel{ID: channelID, DirPath: dirPath}, proxy.ActionRequest{Trigger: proxy.TriggerSchedule, TaskID: taskID})
	}
}

// workflowPluginService is the part of the plugin service workflow plugin
// nodes run on.
type workflowPluginService interface {
	RunAction(ctx context.Context, pluginID, action string, ch runtime.Channel, req proxy.ActionRequest) error
	WorkflowTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error)
}

// workflowPlugins runs workflow plugin nodes on a plugin service.
type workflowPlugins struct {
	svc workflowPluginService
}

func (w workflowPlugins) RunPluginAction(ctx context.Context, pluginID, action, channelID, dirPath, callID string, input json.RawMessage) error {
	req := proxy.ActionRequest{Trigger: proxy.TriggerWorkflow, RunID: callID, Input: input}
	return w.svc.RunAction(ctx, pluginID, action, runtime.Channel{ID: channelID, DirPath: dirPath}, req)
}

func (w workflowPlugins) CallPluginTool(ctx context.Context, pluginID, tool, channelID, dirPath, callID string, input json.RawMessage) (workflow.PluginToolResult, error) {
	res, err := w.svc.WorkflowTool(ctx, pluginID, tool, runtime.Channel{ID: channelID, DirPath: dirPath}, proxy.ToolRequest{RunID: callID, Input: input})
	if err != nil {
		return workflow.PluginToolResult{}, err
	}
	return workflow.PluginToolResult{Content: res.Content, IsError: res.IsError, Structured: res.StructuredContent}, nil
}

// pluginProject returns a plugin's config in a channel's project: the
// project config's plugins.<id> over the global one. A worktree's project
// config is its checkout's, as the config API has it; a channel without a
// dir has its project config where the API keeps it.
func pluginProject(cfg *config.Config, reload func() (*config.Config, error), store db.ChannelGetter) install.Project {
	return func(ctx context.Context, pluginID string, ch runtime.Channel) (config.PluginConfig, error) {
		dir := ch.DirPath
		c, err := store.GetChannel(ctx, ch.ID)
		if err != nil {
			return config.PluginConfig{}, err
		}
		if c != nil {
			dir = cmp.Or(db.WorktreeRootDirPath(ctx, store, c), dir)
		}
		if dir == "" {
			dir = filepath.Join(cfg.LoopDir, ch.ID, "work")
		}
		merged, err := mergedProjectConfig(cfg, reload, dir, "")
		if err != nil {
			return config.PluginConfig{}, err
		}
		return merged.Plugins.ByID[pluginID], nil
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
