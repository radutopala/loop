package dockerproxy

import (
	"path"
	"strings"
)

// Docker Desktop's daemon runs in a VM that sees the host's shared folders
// under /host_mnt, and reports bind sources that way. A tool that inspects
// its own container for the host path of its working directory (pre-commit's
// docker_image hooks do) then asks for binds like
// /host_mnt/Users/<me>/project:/src. The proxy runs in the agent container,
// where that path doesn't exist: the policy can't resolve it, and the
// baseline /host_mnt deny fires even for the agent's own workspace.
//
// unmapHostMnt rewrites such a source to the path the agent sees when it
// lies under one of the agent's own mounts (a BindRoot, as mounted or as its
// host path with symlinks resolved), so the rules, the read-only overlays and
// pinning judge it as they would the same bind written that way. Any other
// /host_mnt source is left for the rules to deny.

// hostMntPrefix is where Docker Desktop's VM mounts the host's shared folders.
const hostMntPrefix = "/host_mnt"

// agentPath maps a /host_mnt bind source under a BindRoot to the path the
// agent sees, and reports whether it did.
func (s *Server) agentPath(src string) (string, bool) {
	rest, ok := strings.CutPrefix(path.Clean(src), hostMntPrefix)
	if !ok || !strings.HasPrefix(rest, "/") {
		return "", false
	}
	for _, root := range s.cfg.BindRoots {
		for _, host := range []string{root, s.cfg.BindHostPaths[root]} {
			if host != "" && (rest == host || strings.HasPrefix(rest, host+"/")) {
				return root + strings.TrimPrefix(rest, host), true
			}
		}
	}
	return "", false
}

// unmapHostMnt rewrites the /host_mnt sources of a create body's binds
// (HostConfig.Binds and bind-type HostConfig.Mounts) that agentPath maps.
// Reports whether anything changed.
func (s *Server) unmapHostMnt(body map[string]any) bool {
	hcv, _ := foldGet(body, "HostConfig")
	hc, _ := hcv.(map[string]any)
	if hc == nil {
		return false
	}
	changed := false
	mv, _ := foldGet(hc, "Mounts")
	mounts, _ := mv.([]any)
	for _, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok || foldString(mm, "Type") != "bind" {
			continue
		}
		if p, ok := s.agentPath(foldString(mm, "Source")); ok {
			key, _ := foldKey(mm, "Source")
			mm[key] = p
			changed = true
		}
	}
	bv, _ := foldGet(hc, "Binds")
	binds, _ := bv.([]any)
	for i, b := range binds {
		str, _ := b.(string)
		src, rest, found := strings.Cut(str, ":")
		if !found {
			continue
		}
		if p, ok := s.agentPath(src); ok {
			binds[i] = p + ":" + rest
			changed = true
		}
	}
	return changed
}
