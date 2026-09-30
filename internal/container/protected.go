package container

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/radutopala/loop/internal/types"
)

// DefaultProtectedDirs returns the host dirs agent containers must never
// reach: loop's dir under the user config dir (the owner token) and
// policyDir (per-container policy files and gate audit logs). A
// userConfigDir error just leaves its dir out.
func DefaultProtectedDirs(policyDir string, userConfigDir func() (string, error)) []string {
	var dirs []string
	if d, err := userConfigDir(); err == nil && d != "" {
		dirs = append(dirs, filepath.Join(d, "loop"))
	}
	if policyDir != "" {
		dirs = append(dirs, filepath.Clean(policyDir))
	}
	return dirs
}

// SetProtectedDirs sets the host dirs no agent container may reach. Each dir
// is kept both as given and with symlinks resolved, so a mount spelled
// through a symlink is caught too.
func (r *DockerRunner) SetProtectedDirs(dirs []string) {
	r.protectedDirs = nil
	for _, d := range dirs {
		r.protectedDirs = appendUnique(r.protectedDirs, d)
		if p, err := r.sys.EvalSymlinks(d); err == nil {
			r.protectedDirs = appendUnique(r.protectedDirs, p)
		}
	}
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// pathsOverlap reports whether a and b are the same path or one is under the
// other, ignoring case (macOS and Windows hosts fold it).
func pathsOverlap(a, b string) bool {
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	return a == b || strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/") || strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/")
}

// dropProtectedBinds removes the host binds that would expose a protected
// dir: the dir itself, anything under it, or an ancestor mounted writable
// (mounting $HOME would hand over ~/.loop/run with it, and a writable one
// lets the agent move the dir aside and read what the daemon writes next).
// A read-only ancestor is kept, with each protected dir under it covered by
// an empty read-only tmpfs; masks returns those container paths. Named
// volumes are left alone.
func (r *DockerRunner) dropProtectedBinds(binds []string) (kept, masks []string) {
	if len(r.protectedDirs) == 0 {
		return binds, nil
	}
	kept = binds[:0:0]
	for _, b := range binds {
		ms, err := parseMountSpec(b)
		if err == nil && strings.HasPrefix(ms.Host, "/") {
			m, d, ok := r.protectedMasks(ms)
			if !ok {
				fmt.Fprintf(os.Stderr, "Warning: skipping mount %s: it exposes %s, which holds loop's credentials\n", ms.Host, d)
				continue
			}
			for _, p := range m {
				masks = appendUnique(masks, p)
			}
		}
		kept = append(kept, b)
	}
	return kept, masks
}

// protectedMasks returns the container paths to cover for a bind whose host
// path is a read-only ancestor of protected dirs. ok is false, with the dir
// it conflicts with, when the bind must be dropped instead: it reaches a
// protected dir some other way, is writable, or a dir to cover is missing
// (Docker would have to create the mount point in a read-only bind).
func (r *DockerRunner) protectedMasks(ms mountSpec) (masks []string, conflict string, ok bool) {
	hosts := []string{filepath.Clean(ms.Host)}
	if p, err := r.sys.EvalSymlinks(ms.Host); err == nil && p != hosts[0] {
		hosts = append(hosts, p)
	}
	readOnly := slices.Contains(strings.Split(ms.Mode, ","), "ro")
	for _, d := range r.protectedDirs {
		for _, h := range hosts {
			if !pathsOverlap(h, d) {
				continue
			}
			rel, under := relUnder(h, d)
			if !under || !readOnly {
				return nil, d, false
			}
			if _, err := r.sys.Stat(d); err != nil {
				return nil, d, false
			}
			masks = appendUnique(masks, path.Join(ms.Container, rel))
		}
	}
	return masks, "", true
}

// relUnder returns dir's path below ancestor, when dir is strictly under
// it, ignoring case like pathsOverlap.
func relUnder(ancestor, dir string) (string, bool) {
	prefix := strings.TrimSuffix(filepath.Clean(ancestor), "/") + "/"
	dir = filepath.Clean(dir)
	if len(dir) <= len(prefix) || !strings.EqualFold(dir[:len(prefix)], prefix) {
		return "", false
	}
	return dir[len(prefix):], true
}

// protectedDirPrefix matches the prefixes Docker Desktop reaches host paths
// through, so a bind of /host_mnt/<dir> is caught like <dir>.
const protectedDirPrefix = `(?i)^(/host_mnt|/run/desktop/mnt/host)?`

// protectedDirBodyRules returns deny rules for nested containers and volumes
// reaching a protected dir, or anything under it. They go ahead of every
// other rule and are never relaxed for the agent's own mounts.
func (r *DockerRunner) protectedDirBodyRules() []types.BodyRule {
	if len(r.protectedDirs) == 0 {
		return nil
	}
	// The dir, anything under it, and its ancestors: a nested container
	// gets no tmpfs over the dir, so a read-only ancestor exposes it too.
	var values []string
	for _, d := range r.protectedDirs {
		d = filepath.Clean(d)
		values = append(values, protectedDirPrefix+regexp.QuoteMeta(strings.TrimSuffix(d, "/"))+`(/|$)`)
		for a := filepath.Dir(d); ; a = filepath.Dir(a) {
			values = appendUnique(values, protectedDirPrefix+regexp.QuoteMeta(strings.TrimSuffix(a, "/"))+`/?$`)
			if a == "/" || a == "." {
				break
			}
		}
	}
	const msg = "loop's credential and policy dirs can't be mounted into containers"
	return []types.BodyRule{
		{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1048576,
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Binds[*]", Op: "path_in", Values: values},
				{Path: "HostConfig.Mounts[*].Source", Op: "path_in", Values: values},
				{Path: "HostConfig.Mounts[*].VolumeOptions.DriverConfig.Options.device", Op: "path_in", Values: values},
			},
			Decision: types.DecisionDeny,
			Message:  msg,
		},
		{
			AppliesTo:    "POST ^/volumes/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1048576,
			JSONChecks: []types.JSONCheck{
				{Path: "DriverOpts.device", Op: "path_in", Values: values},
			},
			Decision: types.DecisionDeny,
			Message:  msg,
		},
	}
}
