package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Containers the agent starts with the docker socket mounted must not reach
// the raw daemon: `-v /var/run/docker.sock:/var/run/docker.sock` names the
// proxy inside the agent container, but the daemon resolves bind sources on
// its own filesystem, where that path is the real engine socket. The proxy
// therefore rewrites such binds to a mount of its second listening socket,
// which lives in an anonymous volume on the agent container. Requests from
// the nested container then pass through the same policy.

const (
	// nestedSocketName is the proxy socket's file name inside the nested
	// volume.
	nestedSocketName = "docker.sock"

	// nestedBodyCap bounds how much of a create body is read to look for
	// socket mounts. Matches the default body rules' MaxBodyBytes.
	nestedBodyCap = 1 << 20

	// minSubpathAPIMinor is the first Docker API version (1.x) that supports
	// VolumeOptions.Subpath. Without it the whole volume directory would be
	// mounted at the socket path.
	minSubpathAPIMinor = 45
)

// nestedFoldNames are the keys rewriteSocketMounts reads; bodies must not
// hold case variants of them.
var nestedFoldNames = []string{"HostConfig", "Mounts", "Binds", "Type", "Source", "Target", "ReadOnly"}

// daemonSocketPaths are the in-container paths of the docker socket. A bind
// whose source is (or resolves to) one of them is a socket mount.
var daemonSocketPaths = map[string]bool{
	"/var/run/docker.sock": true,
	"/run/docker.sock":     true,
}

var apiVersionMinorRe = regexp.MustCompile(`^/v1\.(\d+)(/|$)`)

// isDockerSocket reports whether a bind source names the docker socket,
// either literally or through a symlink.
func (s *Server) isDockerSocket(src string) bool {
	if !strings.HasPrefix(src, "/") {
		return false
	}
	if daemonSocketPaths[path.Clean(src)] {
		return true
	}
	if s.cfg.EvalSymlinks == nil {
		return false
	}
	resolved, err := s.cfg.EvalSymlinks(src)
	return err == nil && daemonSocketPaths[resolved]
}

// rewriteNestedSocket replaces docker socket mounts in a POST
// /containers/create body with a mount of the nested proxy socket, and keeps
// the ReadOnlyDirs read-only under the body's binds (protectReadOnlyDirs).
// Docker Desktop /host_mnt sources under the agent's mounts are first mapped
// to the paths the agent sees (unmapHostMnt). It
// returns a non-zero status (with a message) when the request must be
// rejected: socket mounts fail closed when the nested socket is unavailable,
// so they never reach the daemon as raw binds. Bodies it can't read or
// parse are left for evaluateBody to handle.
func (s *Server) rewriteNestedSocket(r *http.Request) (int, string) {
	if r.Body == nil || r.Body == http.NoBody || normalizeContentType(r.Header.Get("Content-Type")) != "application/json" {
		return 0, ""
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, nestedBodyCap+1))
	if err != nil {
		return http.StatusBadRequest, "invalid request body"
	}
	_ = r.Body.Close()
	restore := func(b []byte) {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	if len(buf) > nestedBodyCap {
		return http.StatusRequestEntityTooLarge, "container create body too large"
	}
	restore(buf)

	dec := json.NewDecoder(bytes.NewReader(buf))
	// Keep numbers verbatim so byte counts and the like survive the
	// re-encode without float rounding.
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return 0, ""
	}
	if hasFoldDuplicates(body, s.foldNames) {
		return http.StatusBadRequest, errAmbiguousKeys.Error()
	}
	hostMnt := s.unmapHostMnt(body)
	socket := rewriteSocketMounts(body, s.isDockerSocket, s.cfg.NestedVolume)
	if socket && s.cfg.NestedVolume == "" {
		return http.StatusForbidden, "docker socket mounts are unavailable: the nested proxy socket is not configured"
	}
	if m := apiVersionMinorRe.FindStringSubmatch(r.URL.Path); socket && m != nil {
		if minor, _ := strconv.Atoi(m[1]); minor < minSubpathAPIMinor {
			return http.StatusBadRequest, fmt.Sprintf("docker socket mounts need Docker API >= 1.%d", minSubpathAPIMinor)
		}
	}
	if !s.protectReadOnlyDirs(body) && !socket && !hostMnt {
		return 0, ""
	}
	out, _ := json.Marshal(body)
	restore(out)
	return 0, ""
}

// rewriteSocketMounts moves docker socket binds (HostConfig.Binds and
// bind-type HostConfig.Mounts) to volume mounts of the proxy socket in
// volume, keeping each mount's target and read-only flag. Keys match
// case-insensitively, as the daemon's JSON decoding does (see fold.go).
// Reports whether anything
// was rewritten.
func rewriteSocketMounts(body map[string]any, isSocket func(string) bool, volume string) bool {
	hcv, _ := foldGet(body, "HostConfig")
	hc, _ := hcv.(map[string]any)
	if hc == nil {
		return false
	}
	mountsKey, hasMounts := foldKey(hc, "Mounts")
	mounts, _ := hc[mountsKey].([]any)
	changed := false
	for i, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok || foldString(mm, "Type") != "bind" || !isSocket(foldString(mm, "Source")) {
			continue
		}
		ro, _ := foldGet(mm, "ReadOnly")
		readOnly := ro == true
		mounts[i] = socketMount(volume, foldString(mm, "Target"), readOnly)
		changed = true
	}
	if bindsKey, ok := foldKey(hc, "Binds"); ok {
		binds, _ := hc[bindsKey].([]any)
		kept := make([]any, 0, len(binds))
		for _, b := range binds {
			str, _ := b.(string)
			src, rest, found := strings.Cut(str, ":")
			if !found || !isSocket(src) {
				kept = append(kept, b)
				continue
			}
			target, opts, _ := strings.Cut(rest, ":")
			mounts = append(mounts, socketMount(volume, target, hasMountOption(opts, "ro")))
			changed = true
		}
		hc[bindsKey] = kept
	}
	if changed {
		if !hasMounts {
			mountsKey = "Mounts"
		}
		hc[mountsKey] = mounts
	}
	return changed
}

// protectReadOnlyDirs keeps s.cfg.ReadOnlyDirs read-only in the container
// being created: a read-write bind that contains one gets a read-only bind
// of it on top, and a read-write bind inside one becomes read-only. Nested
// containers aren't subject to the agent's file-op rules, so without this a
// workspace bind could rewrite the project config the next agent container
// is built from. Sources are compared after symlink resolution; ones that
// don't resolve are left to the body rules. Reports whether the body
// changed.
func (s *Server) protectReadOnlyDirs(body map[string]any) bool {
	if len(s.cfg.ReadOnlyDirs) == 0 {
		return false
	}
	hcv, _ := foldGet(body, "HostConfig")
	hc, _ := hcv.(map[string]any)
	if hc == nil {
		return false
	}
	resolve := func(src string) (string, bool) {
		if !strings.HasPrefix(src, "/") {
			return "", false
		}
		if s.cfg.EvalSymlinks == nil {
			return path.Clean(src), true
		}
		r, err := s.cfg.EvalSymlinks(src)
		return path.Clean(r), err == nil
	}
	// covers returns the read-only mounts a read-write bind of src at target
	// needs, and whether the bind itself lies inside a read-only dir.
	covers := func(src, target string) (overlays []any, inside bool) {
		resolved, ok := resolve(src)
		if !ok {
			return nil, false
		}
		for _, dir := range s.cfg.ReadOnlyDirs {
			switch {
			case resolved == dir || strings.HasPrefix(resolved, dir+"/"):
				return nil, true
			case strings.HasPrefix(dir, resolved+"/") || resolved == "/":
				overlays = append(overlays, map[string]any{
					"Type":     "bind",
					"Source":   dir,
					"Target":   path.Join(target, strings.TrimPrefix(dir, resolved)),
					"ReadOnly": true,
				})
			}
		}
		return overlays, false
	}

	changed := false
	var extra []any
	mountsKey, hasMounts := foldKey(hc, "Mounts")
	mounts, _ := hc[mountsKey].([]any)
	for _, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok || foldString(mm, "Type") != "bind" {
			continue
		}
		if ro, _ := foldGet(mm, "ReadOnly"); ro == true {
			continue
		}
		overlays, inside := covers(foldString(mm, "Source"), foldString(mm, "Target"))
		if inside {
			key, ok := foldKey(mm, "ReadOnly")
			if !ok {
				key = "ReadOnly"
			}
			mm[key] = true
			changed = true
		}
		extra = append(extra, overlays...)
	}
	if bindsKey, ok := foldKey(hc, "Binds"); ok {
		binds, _ := hc[bindsKey].([]any)
		for i, b := range binds {
			str, _ := b.(string)
			src, rest, found := strings.Cut(str, ":")
			target, opts, _ := strings.Cut(rest, ":")
			if !found || hasMountOption(opts, "ro") {
				continue
			}
			overlays, inside := covers(src, target)
			if inside {
				binds[i] = src + ":" + target + ":" + readOnlyOptions(opts)
				changed = true
			}
			extra = append(extra, overlays...)
		}
	}
	if len(extra) > 0 {
		if !hasMounts {
			mountsKey = "Mounts"
		}
		hc[mountsKey] = append(mounts, extra...)
		changed = true
	}
	return changed
}

// readOnlyOptions turns a bind's option list read-only: "rw" is dropped and
// "ro" added, other options (z, Z, propagation) are kept.
func readOnlyOptions(opts string) string {
	out := []string{"ro"}
	for o := range strings.SplitSeq(opts, ",") {
		if o != "" && o != "rw" {
			out = append(out, o)
		}
	}
	return strings.Join(out, ",")
}

// socketMount is a HostConfig.Mounts entry that mounts the proxy socket from
// volume at target.
func socketMount(volume, target string, readOnly bool) map[string]any {
	return map[string]any{
		"Type":          "volume",
		"Source":        volume,
		"Target":        target,
		"ReadOnly":      readOnly,
		"VolumeOptions": map[string]any{"Subpath": nestedSocketName},
	}
}

// hasMountOption reports whether a bind's comma-separated option list
// contains opt.
func hasMountOption(opts, opt string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == opt {
			return true
		}
	}
	return false
}

// lookupNestedVolume asks the daemon, over the upstream socket, for the name
// of the anonymous volume the container cid has mounted at dir.
func lookupNestedVolume(ctx context.Context, upstream, cid, dir string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", upstream)
			},
		},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+url.PathEscape(cid)+"/json", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", cid, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inspect %s: status %d", cid, resp.StatusCode)
	}
	var info struct {
		Mounts []struct {
			Type        string
			Name        string
			Destination string
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("inspect %s: %w", cid, err)
	}
	for _, m := range info.Mounts {
		if m.Type == "volume" && m.Destination == dir {
			return m.Name, nil
		}
	}
	return "", fmt.Errorf("inspect %s: no volume mounted at %s", cid, dir)
}
