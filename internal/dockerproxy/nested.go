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
// /containers/create body with a mount of the nested proxy socket. It
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
	if hasFoldDuplicates(body) {
		// The daemon keeps one of the variants; which one depends on key
		// order, which the decoded map has lost.
		return http.StatusBadRequest, "ambiguous request body: keys differ only in case"
	}
	if !rewriteSocketMounts(body, s.isDockerSocket, s.cfg.NestedVolume) {
		return 0, ""
	}
	if s.cfg.NestedVolume == "" {
		return http.StatusForbidden, "docker socket mounts are unavailable: the nested proxy socket is not configured"
	}
	if m := apiVersionMinorRe.FindStringSubmatch(r.URL.Path); m != nil {
		if minor, _ := strconv.Atoi(m[1]); minor < minSubpathAPIMinor {
			return http.StatusBadRequest, fmt.Sprintf("docker socket mounts need Docker API >= 1.%d", minSubpathAPIMinor)
		}
	}
	out, _ := json.Marshal(body)
	restore(out)
	return 0, ""
}

// rewriteSocketMounts moves docker socket binds (HostConfig.Binds and
// bind-type HostConfig.Mounts) to volume mounts of the proxy socket in
// volume, keeping each mount's target and read-only flag. Keys match
// case-insensitively, as the daemon's JSON decoding does; callers reject
// bodies with case-variant duplicate keys first. Reports whether anything
// was rewritten.
func rewriteSocketMounts(body map[string]any, isSocket func(string) bool, volume string) bool {
	hc, _ := body[foldKey(body, "HostConfig")].(map[string]any)
	if hc == nil {
		return false
	}
	mountsKey := foldKey(hc, "Mounts")
	mounts, _ := hc[mountsKey].([]any)
	changed := false
	for i, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok || foldString(mm, "Type") != "bind" || !isSocket(foldString(mm, "Source")) {
			continue
		}
		readOnly := mm[foldKey(mm, "ReadOnly")] == true
		mounts[i] = socketMount(volume, foldString(mm, "Target"), readOnly)
		changed = true
	}
	bindsKey := foldKey(hc, "Binds")
	if binds, ok := hc[bindsKey].([]any); ok {
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
		if mountsKey == "" {
			mountsKey = "Mounts"
		}
		hc[mountsKey] = mounts
	}
	return changed
}

// foldKey returns the key of m equal to key under case folding, or "" when
// there is none.
func foldKey(m map[string]any, key string) string {
	for k := range m {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	return ""
}

// foldString returns the string under key (case-insensitive) in m, empty
// when absent or not a string.
func foldString(m map[string]any, key string) string {
	v, _ := m[foldKey(m, key)].(string)
	return v
}

// hasFoldDuplicates reports whether any object in v has two keys equal
// under case folding.
func hasFoldDuplicates(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		seen := make(map[string]bool, len(t))
		for k, child := range t {
			lk := strings.ToLower(k)
			if seen[lk] || hasFoldDuplicates(child) {
				return true
			}
			seen[lk] = true
		}
	case []any:
		for _, child := range t {
			if hasFoldDuplicates(child) {
				return true
			}
		}
	}
	return false
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
