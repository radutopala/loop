package dockerproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
)

// pinBinds turns a bind under the agent's mounts into a mount of a loop-bind
// volume, so the daemon reports it that way: GET /containers/{id}/json lists
// it in Mounts as a volume whose Source is the volume's directory in the
// daemon's data root (/var/lib/docker/volumes/loop-bind-<hash>/_data), not
// the path the agent bound. A tool that checks a container's mount against
// its working directory (a dev script asking whether the running dev
// container already mounts $PWD) never finds a match, and recreates the
// container on every call.
//
// unpinInspect rewrites those mounts in the inspect response back to the
// binds the agent asked for: the pinned root joined with the mount's
// Subpath, which the response still carries in HostConfig.Mounts.

// containerInspectRe matches GET /containers/{id}/json (canonical path).
var containerInspectRe = regexp.MustCompile(`^/containers/[^/]+/json$`)

// modifyResponse is the upstream proxy's ModifyResponse hook.
func (s *Server) modifyResponse(resp *http.Response) error {
	if len(s.cfg.BindRoots) == 0 || resp.StatusCode != http.StatusOK || resp.Request.Method != http.MethodGet ||
		!containerInspectRe.MatchString(stripAPIVersionPrefix(resp.Request.URL.Path)) ||
		normalizeContentType(resp.Header.Get("Content-Type")) != "application/json" {
		return nil
	}
	buf, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	out := buf
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.UseNumber()
	var body map[string]any
	if dec.Decode(&body) == nil && s.unpinInspect(body) {
		out, _ = json.Marshal(body)
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// unpinInspect rewrites the loop-bind volume mounts of a container inspect
// body (Mounts and HostConfig.Mounts) to binds of the paths they pin.
// Reports whether anything changed.
func (s *Server) unpinInspect(body map[string]any) bool {
	roots := make(map[string]string, len(s.cfg.BindRoots))
	for _, r := range s.cfg.BindRoots {
		roots[bindVolumeName(r)] = r
	}
	// Mounts has no Subpath; HostConfig.Mounts does, keyed by target.
	byTarget := map[string]string{}
	changed := false
	hc, _ := body["HostConfig"].(map[string]any)
	hcMounts, _ := hc["Mounts"].([]any)
	for i, m := range hcMounts {
		mm, _ := m.(map[string]any)
		src, _ := mm["Source"].(string)
		root, ok := roots[src]
		if mm["Type"] != "volume" || !ok {
			continue
		}
		opts, _ := mm["VolumeOptions"].(map[string]any)
		sub, _ := opts["Subpath"].(string)
		p := path.Join(root, sub)
		target, _ := mm["Target"].(string)
		byTarget[target] = p
		bind := map[string]any{"Type": "bind", "Source": p, "Target": target}
		if ro, ok := mm["ReadOnly"]; ok {
			bind["ReadOnly"] = ro
		}
		hcMounts[i] = bind
		changed = true
	}
	mounts, _ := body["Mounts"].([]any)
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		name, _ := mm["Name"].(string)
		root, ok := roots[name]
		if mm["Type"] != "volume" || !ok {
			continue
		}
		dest, _ := mm["Destination"].(string)
		p, ok := byTarget[dest]
		if !ok {
			p = root
		}
		mm["Type"] = "bind"
		mm["Source"] = p
		mm["Mode"] = ""
		delete(mm, "Name")
		delete(mm, "Driver")
		changed = true
	}
	return changed
}
