package dockerproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
)

// The policy checks a bind's source when the container is created, but the
// daemon mounts it when the container starts, following any symlink it
// meets on the way. Between the two the agent can swap a checked directory
// for a symlink, and the container gets whatever that points to.
//
// pinBinds closes the gap for binds under the agent's own mounts (BindRoots)
// by moving them to a named volume bound to the root, with the rest of the
// path as VolumeOptions.Subpath. The daemon opens a subpath one component at
// a time, beneath the volume, at mount time and mounts the directory it
// opened, so a swapped-in symlink that leads out of the root fails the start
// instead. The roots themselves can't be swapped: their parents aren't
// mounted in the agent container. Binds outside the roots were approved as
// the path they resolved to, so they are forwarded as that path.

const (
	// bindVolumePrefix names the volumes pinBinds binds to a root. Requests
	// from the agent may not create or mount volumes with it (see
	// reservesBindVolume and pinBinds).
	bindVolumePrefix = "loop-bind-"
	// BindVolumeLabel is the value of the "app" label on those volumes;
	// loop removes the unused ones when it removes a container.
	BindVolumeLabel = "loop-bind"
)

// bindVolumeName is the volume pinBinds binds to root.
func bindVolumeName(root string) string {
	sum := sha256.Sum256([]byte(root))
	return bindVolumePrefix + hex.EncodeToString(sum[:8])
}

// pinRoot returns the outermost BindRoot holding resolved, and resolved's
// path below it. The outermost one, because an inner root is a directory
// of an outer one that a container binding the outer root could swap.
func (s *Server) pinRoot(resolved string) (root, rel string, ok bool) {
	for _, r := range s.cfg.BindRoots {
		if resolved == r || strings.HasPrefix(resolved, r+"/") {
			return r, strings.TrimPrefix(strings.TrimPrefix(resolved, r), "/"), true
		}
	}
	return "", "", false
}

// pinnedBind is a host-path bind pinBinds rewrites.
type pinnedBind struct {
	resolved string
	target   string
	readOnly bool
}

// pinBinds rewrites the host-path binds of a POST /containers/create body
// (see the comment above). It runs after the policy has approved the
// request, and returns a non-zero status (with a message) when the request
// must be rejected instead: a mount of a pinned-bind volume by name, a
// source that doesn't resolve, a pinned bind sent at an API version without
// Subpath, or a pin volume the daemon couldn't provide.
func (s *Server) pinBinds(r *http.Request) (int, string) {
	if len(s.cfg.BindRoots) == 0 || s.cfg.EvalSymlinks == nil || r.Body == nil || r.Body == http.NoBody ||
		normalizeContentType(r.Header.Get("Content-Type")) != "application/json" {
		return 0, ""
	}
	buf, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	restore := func(b []byte) {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	restore(buf)
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.UseNumber()
	var body map[string]any
	if dec.Decode(&body) != nil {
		return 0, ""
	}
	hcv, _ := foldGet(body, "HostConfig")
	hc, _ := hcv.(map[string]any)
	if hc == nil {
		return 0, ""
	}

	var pins []pinnedBind
	resolveBind := func(src, target string, readOnly bool) (int, string) {
		resolved, err := s.cfg.EvalSymlinks(src)
		if err != nil {
			return http.StatusBadRequest, fmt.Sprintf("bind source %s: %v (sources are checked before the container is created, so they must exist)", src, err)
		}
		pins = append(pins, pinnedBind{resolved: path.Clean(resolved), target: target, readOnly: readOnly})
		return 0, ""
	}

	mountsKey, hasMounts := foldKey(hc, "Mounts")
	mounts, _ := hc[mountsKey].([]any)
	kept := make([]any, 0, len(mounts))
	for _, m := range mounts {
		mm, ok := m.(map[string]any)
		src := foldString(mm, "Source")
		if strings.HasPrefix(src, bindVolumePrefix) {
			return http.StatusForbidden, reservedVolumeMsg
		}
		if !ok || foldString(mm, "Type") != "bind" || !strings.HasPrefix(src, "/") {
			kept = append(kept, m)
			continue
		}
		ro, _ := foldGet(mm, "ReadOnly")
		if status, msg := resolveBind(src, foldString(mm, "Target"), ro == true); status != 0 {
			return status, msg
		}
	}
	bindsKey, hasBinds := foldKey(hc, "Binds")
	binds, _ := hc[bindsKey].([]any)
	keptBinds := make([]any, 0, len(binds))
	for _, b := range binds {
		str, _ := b.(string)
		src, rest, found := strings.Cut(str, ":")
		if strings.HasPrefix(src, bindVolumePrefix) {
			return http.StatusForbidden, reservedVolumeMsg
		}
		if !found || !strings.HasPrefix(src, "/") {
			keptBinds = append(keptBinds, b)
			continue
		}
		target, opts, _ := strings.Cut(rest, ":")
		if status, msg := resolveBind(src, target, hasMountOption(opts, "ro")); status != 0 {
			return status, msg
		}
	}
	if len(pins) == 0 {
		return 0, ""
	}

	needSubpath := false
	for _, p := range pins {
		if _, rel, ok := s.pinRoot(p.resolved); ok && rel != "" {
			needSubpath = true
		}
	}
	if m := apiVersionMinorRe.FindStringSubmatch(r.URL.Path); needSubpath && m != nil {
		if minor, _ := strconv.Atoi(m[1]); minor < minSubpathAPIMinor {
			return http.StatusBadRequest, fmt.Sprintf("binds under the agent's mounts need Docker API >= 1.%d", minSubpathAPIMinor)
		}
	}

	ensured := map[string]bool{}
	for _, p := range pins {
		root, rel, ok := s.pinRoot(p.resolved)
		if !ok {
			kept = append(kept, map[string]any{"Type": "bind", "Source": p.resolved, "Target": p.target, "ReadOnly": p.readOnly})
			continue
		}
		if !ensured[root] {
			if err := s.ensureBindVolume(r.Context(), root, rel != ""); err != nil {
				return http.StatusBadGateway, err.Error()
			}
			ensured[root] = true
		}
		// The driver config and labels recreate the volume as ensured
		// should it be removed (by a prune) before the container is
		// created; the daemon ignores them for a volume that exists.
		opts := map[string]any{
			"NoCopy":       true,
			"DriverConfig": map[string]any{"Name": "local", "Options": bindVolumeOptions(root)},
			"Labels":       map[string]any{"app": BindVolumeLabel},
		}
		if rel != "" {
			opts["Subpath"] = rel
		}
		kept = append(kept, map[string]any{
			"Type":          "volume",
			"Source":        bindVolumeName(root),
			"Target":        p.target,
			"ReadOnly":      p.readOnly,
			"VolumeOptions": opts,
		})
	}
	if !hasMounts {
		mountsKey = "Mounts"
	}
	hc[mountsKey] = kept
	if hasBinds {
		hc[bindsKey] = keptBinds
	}
	out, _ := json.Marshal(body)
	restore(out)
	return 0, ""
}

// ensureBindVolume creates (or finds) the volume bound to root. Creating an
// existing volume returns it unchanged whatever the options asked for, so
// the returned options are checked (boundTo): a volume of that name bound
// elsewhere would otherwise mount the wrong directory. With subpath, the daemon must
// also support VolumeOptions.Subpath: an older one ignores it and mounts
// the whole root.
func (s *Server) ensureBindVolume(ctx context.Context, root string, subpath bool) error {
	name := bindVolumeName(root)
	payload, _ := json.Marshal(map[string]any{
		"Name":       name,
		"Driver":     "local",
		"DriverOpts": bindVolumeOptions(root),
		"Labels":     map[string]string{"app": BindVolumeLabel},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/volumes/create", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("creating bind volume for %s: %w", root, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("creating bind volume for %s: status %d: %s", root, resp.StatusCode, bytes.TrimSpace(msg))
	}
	var vol struct {
		Driver  string
		Options map[string]string
	}
	if err := json.NewDecoder(resp.Body).Decode(&vol); err != nil {
		return fmt.Errorf("creating bind volume for %s: %w", root, err)
	}
	if vol.Driver != "local" || !boundTo(vol.Options, root, s.cfg.BindHostPaths[root]) {
		return fmt.Errorf("volume %s exists but isn't bound to %s; remove it", name, root)
	}
	if subpath {
		v := resp.Header.Get("Api-Version")
		minor, err := strconv.Atoi(strings.TrimPrefix(v, "1."))
		if !strings.HasPrefix(v, "1.") || err != nil || minor < minSubpathAPIMinor {
			return fmt.Errorf("binds under the agent's mounts need a Docker daemon with API >= 1.%d (got %q)", minSubpathAPIMinor, v)
		}
	}
	return nil
}

// bindVolumeOptions are the local driver options that bind a volume to root.
func bindVolumeOptions(root string) map[string]string {
	return map[string]string{"type": "none", "o": "bind", "device": root}
}

// boundTo reports whether a local volume's options bind it to root, or to
// hostPath, root's host path with symlinks resolved ("" when it's root).
// Docker Desktop reports the device as its VM sees the host: resolved, and
// under /host_mnt.
func boundTo(opts map[string]string, root, hostPath string) bool {
	if len(opts) != 3 || opts["type"] != "none" || opts["o"] != "bind" {
		return false
	}
	for _, p := range []string{root, hostPath} {
		if p != "" && (opts["device"] == p || opts["device"] == "/host_mnt"+p) {
			return true
		}
	}
	return false
}

// reservesBindVolume reports whether a POST /volumes/create request names a
// volume in pinBinds' namespace, under any case variant of the key since
// the daemon matches keys case-insensitively. The agent could otherwise
// create one bound elsewhere ahead of the proxy; ensureBindVolume would
// refuse it, but every pinned bind under that root would then fail. It
// runs after evaluateBody, which leaves the body buffered; bodies that
// aren't JSON objects are left to the daemon. The body is restored for
// forwarding.
func reservesBindVolume(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody || normalizeContentType(r.Header.Get("Content-Type")) != "application/json" {
		return false
	}
	buf, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))
	var body map[string]any
	if json.Unmarshal(buf, &body) != nil {
		return false
	}
	for k, v := range body {
		if name, _ := v.(string); strings.EqualFold(k, "Name") && strings.HasPrefix(name, bindVolumePrefix) {
			return true
		}
	}
	return false
}

// reservedVolumeMsg rejects a request that names a pinned-bind volume.
const reservedVolumeMsg = "volume names starting with " + bindVolumePrefix + " are reserved for the docker proxy"

// sortRoots orders roots outermost first, for pinRoot.
func sortRoots(roots []string) []string {
	out := slices.Clone(roots)
	slices.SortStableFunc(out, func(a, b string) int { return len(a) - len(b) })
	return out
}
