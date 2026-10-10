package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"

	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/types"
)

// GateConfig runs plugin containers under agentgate.
type GateConfig struct {
	// Binary returns the host path of the linux loop binary the gate runs
	// from; a plugin image doesn't have one.
	Binary func(ctx context.Context) (string, error)
	// AuditRetentionDays and AuditVerbose configure the gate's audit log.
	AuditRetentionDays int
	AuditVerbose       bool
}

const (
	// gateBinary is where the gate's loop binary is mounted.
	gateBinary = "/opt/loop/loop"
	// gatePolicy is where a container's gate policy is mounted.
	gatePolicy = "/etc/loop/gate-policy.json"
	// gateAuditDir is where a container's gate audit dir is mounted.
	gateAuditDir = "/var/log/loop-gate"
)

// gateCaps are the capabilities the gate's root parent needs: to trace the
// plugin's syscalls, drop to the image's user, and write its audit log into
// a dir the daemon's user owns. The plugin loses them with the drop.
var gateCaps = []string{"SYS_PTRACE", "SETUID", "SETGID", "DAC_OVERRIDE"}

// gatePolicyFile is the policy loop syscallwrap reads.
type gatePolicyFile struct {
	DefaultDecision types.Decision      `json:"default_decision"`
	PathRules       []types.PathRule    `json:"path_rules"`
	CommandRules    []types.CommandRule `json:"command_rules"`
	FileRules       []types.FileRule    `json:"file_rules"`
}

// pluginGatePolicy is a plugin's gate policy: the rules its manifest asks
// for, then Loop's baseline. Anything neither allows is denied, and with
// nobody to ask, so is anything a rule sends for approval.
func pluginGatePolicy(gate *plugin.Gate) gatePolicyFile {
	var files []types.FileRule
	commands := []types.CommandRule{}
	if gate != nil {
		files = append(files, gate.Files...)
		commands = append(commands, gate.Commands...)
	}
	files = append(files,
		types.FileRule{Paths: []string{dataDir, dataDir + "/**", "/tmp", "/tmp/**"}, Decision: types.DecisionAllow},
		types.FileRule{Paths: []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/tty", "/dev/stdout", "/dev/stderr", "/dev/fd/**", "/dev/pts/**", "/dev/shm/**"}, Decision: types.DecisionAllow},
		types.FileRule{Operations: []string{"read", "stat", "list"}, Decision: types.DecisionAllow},
	)
	commands = append(commands, types.CommandRule{Decision: types.DecisionAllow})
	return gatePolicyFile{DefaultDecision: types.DecisionDeny, PathRules: []types.PathRule{}, CommandRules: commands, FileRules: files}
}

// applyGate makes cfg run p's image command under agentgate: as root, in
// loop syscallwrap, which drops to the image's user before running it.
func (m *Manager) applyGate(ctx context.Context, p Plugin, ch Channel, cfg *containertypes.Config, host *containertypes.HostConfig) error {
	bin, err := m.cfg.Gate.Binary(ctx)
	if err != nil {
		return fmt.Errorf("finding the plugin gate: %w", err)
	}
	info, err := m.api.ImageInspect(ctx, p.Image)
	if err != nil {
		return fmt.Errorf("inspecting the plugin image: %w", err)
	}
	if info.Config == nil || info.Config.User == "" {
		return errors.New("the plugin image has no USER to run as")
	}
	argv := append(append([]string{}, info.Config.Entrypoint...), info.Config.Cmd...)
	if len(argv) == 0 {
		return errors.New("the plugin image has no command")
	}

	id := p.Manifest.ID
	dir := m.gateDir(id, ch.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating the plugin gate dir: %w", err)
	}
	raw, _ := json.Marshal(pluginGatePolicy(p.Manifest.Gate))
	policy := filepath.Join(dir, "gate-policy.json")
	if err := os.WriteFile(policy, raw, 0o644); err != nil {
		return fmt.Errorf("writing the plugin gate policy: %w", err)
	}
	audit := m.auditDir(id, ch.ID)
	if err := os.MkdirAll(audit, 0o770); err != nil {
		return fmt.Errorf("creating the plugin gate audit dir: %w", err)
	}

	cfg.User = "0"
	cfg.Entrypoint = append([]string{gateBinary, "syscallwrap", "--"}, argv...)
	cfg.Cmd = nil
	cfg.Env = append(cfg.Env,
		"LOOP_GATE_ENABLED=1",
		"LOOP_GATE_POLICY_FILE="+gatePolicy,
		"LOOP_GATE_APPROVER=deny",
		"LOOP_HOST_USER="+info.Config.User,
		"LOOP_GATE_AUDIT_DIR="+gateAuditDir,
		"LOOP_GATE_AUDIT_RETENTION_DAYS="+strconv.Itoa(m.cfg.Gate.AuditRetentionDays),
	)
	if m.cfg.Gate.AuditVerbose {
		cfg.Env = append(cfg.Env, "LOOP_GATE_AUDIT_VERBOSE=1")
	}
	host.SecurityOpt = append(host.SecurityOpt, "seccomp=unconfined")
	host.CapAdd = gateCaps
	host.Mounts = append(host.Mounts,
		mount.Mount{Type: mount.TypeBind, Source: bin, Target: gateBinary, ReadOnly: true},
		mount.Mount{Type: mount.TypeBind, Source: policy, Target: gatePolicy, ReadOnly: true},
		mount.Mount{Type: mount.TypeBind, Source: audit, Target: gateAuditDir},
	)
	return nil
}

func (m *Manager) gateDir(pluginID, channelID string) string {
	return filepath.Join(m.cfg.Root, pluginID, "gate", channelID)
}

func (m *Manager) auditDir(pluginID, channelID string) string {
	return filepath.Join(m.cfg.Root, pluginID, "audit", channelID)
}
