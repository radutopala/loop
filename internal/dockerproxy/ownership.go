package dockerproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// ChannelLabel is the label loop puts on every agent container, naming the
// channel it serves (container.ChannelLabelKey).
const ChannelLabel = "loop-channel"

// containerPathRe splits a per-container path into the container reference
// and what follows it: /containers/{id}[/...].
var containerPathRe = regexp.MustCompile(`^/containers/([^/]+)(/.*)?$`)

// containerCollectionOps are the /containers/<op> endpoints that name no
// container.
var containerCollectionOps = map[string]bool{"json": true, "create": true, "prune": true}

// attachPathRe matches the attach endpoints, capturing the container.
var attachPathRe = regexp.MustCompile(`^/containers/([^/]+)/attach(/ws)?$`)

// targetContainer returns the container a request acts on: the {id} of a
// per-container path, or the container query parameter of POST /commit.
func targetContainer(method, canonicalPath string, query url.Values) string {
	if canonicalPath == "/commit" && method == http.MethodPost {
		return query.Get("container")
	}
	m := containerPathRe.FindStringSubmatch(canonicalPath)
	if m == nil || (m[2] == "" && containerCollectionOps[m[1]]) {
		return ""
	}
	return m[1]
}

// otherChannelContainer checks the container a request targets against this
// proxy's channel. Another channel's agent container is off-limits whatever
// the rules say: its workspace, tokens and session are that channel's, not
// this agent's. It returns a non-empty reason to deny: the container belongs
// to another channel, or its owner couldn't be established. A container the
// daemon doesn't know passes, so the daemon answers the request with its own
// 404.
func (s *Server) otherChannelContainer(ctx context.Context, id string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+url.PathEscape(id)+"/json", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Sprintf("can't check the owner of container %s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ""
	}
	var body struct {
		Config struct {
			Labels map[string]string
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("can't check the owner of container %s: status %d", id, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Sprintf("can't check the owner of container %s: %v", id, err)
	}
	if ch, ok := body.Config.Labels[ChannelLabel]; ok && ch != s.cfg.ChannelID {
		return "container belongs to another loop channel"
	}
	return ""
}

// recordCreated remembers the container a successful create returned, under
// its ID and the name it was created with, so this agent can attach to it
// without a prompt (see ownsContainer).
func (s *Server) recordCreated(resp *http.Response, body []byte) {
	var created struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(body, &created) != nil || created.ID == "" {
		return
	}
	s.ownedMu.Lock()
	defer s.ownedMu.Unlock()
	s.owned[created.ID] = true
	if name := strings.TrimPrefix(resp.Request.URL.Query().Get("name"), "/"); name != "" {
		s.owned[name] = true
	}
}

// ownsContainer reports whether ref names a container this proxy created: its
// full ID, a unique-enough prefix of it (12+ characters, as the CLI prints),
// or its name.
func (s *Server) ownsContainer(ref string) bool {
	ref = strings.TrimPrefix(ref, "/")
	s.ownedMu.Lock()
	defer s.ownedMu.Unlock()
	if s.owned[ref] {
		return true
	}
	if len(ref) < 12 {
		return false
	}
	for id := range s.owned {
		if strings.HasPrefix(id, ref) {
			return true
		}
	}
	return false
}

// ownedAttach relaxes an approve rule on attach to allow when the container is
// one this agent created: `docker run` and `compose up` attach to theirs
// right after creating it, and a prompt each time would stop them working.
// Attaching to anyone else's container still asks.
func (s *Server) ownedAttach(res HTTPMatchResult, canonicalPath string) HTTPMatchResult {
	if res.Decision != types.DecisionApprove {
		return res
	}
	m := attachPathRe.FindStringSubmatch(canonicalPath)
	if m == nil || !s.ownsContainer(m[1]) {
		return res
	}
	return HTTPMatchResult{Decision: types.DecisionAllow, RuleID: "owned-attach"}
}

// namedVolumes returns the names of the volumes a container create body
// mounts by name: named-volume Binds ("name:/target") and volume-type
// Mounts. The proxy's own nested-socket volume is left out.
func (s *Server) namedVolumes(body any) []string {
	obj, _ := body.(map[string]any)
	hcVal, _ := foldGet(obj, "HostConfig")
	hc, _ := hcVal.(map[string]any)
	var names []string
	bindsVal, _ := foldGet(hc, "Binds")
	binds, _ := bindsVal.([]any)
	for _, b := range binds {
		bs, _ := b.(string)
		if src := extractSourcePath(bs); src != "" && !strings.HasPrefix(src, "/") && src != s.cfg.NestedVolume {
			names = append(names, src)
		}
	}
	mountsVal, _ := foldGet(hc, "Mounts")
	mounts, _ := mountsVal.([]any)
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		if !strings.EqualFold(foldString(mm, "Type"), "volume") {
			continue
		}
		if src := foldString(mm, "Source"); src != "" && src != s.cfg.NestedVolume {
			names = append(names, src)
		}
	}
	return names
}

// deviceVolume returns the first of names whose volume sets a device driver
// option — a local volume bound to a host path or disk, which mounting hands
// the container the same way a bind would. A volume the daemon doesn't know
// is skipped: the create makes it fresh, without options. An error means a
// volume couldn't be checked.
func (s *Server) deviceVolume(ctx context.Context, names []string) (string, error) {
	for _, name := range names {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/volumes/"+url.PathEscape(name), nil)
		resp, err := s.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("inspecting volume %s: %w", name, err)
		}
		var vol struct{ Options map[string]string }
		status := resp.StatusCode
		decodeErr := json.NewDecoder(resp.Body).Decode(&vol)
		_ = resp.Body.Close()
		switch {
		case status == http.StatusNotFound:
			continue
		case status != http.StatusOK:
			return "", fmt.Errorf("inspecting volume %s: status %d", name, status)
		case decodeErr != nil:
			return "", fmt.Errorf("inspecting volume %s: %w", name, decodeErr)
		}
		for k, v := range vol.Options {
			if strings.EqualFold(k, "device") && v != "" {
				return name, nil
			}
		}
	}
	return "", nil
}

// approveDeviceVolumes asks the user before a container create mounts, by
// name, a volume bound to a host device or path (see deviceVolume). Returns
// false when the request was refused, with the response written.
func (s *Server) approveDeviceVolumes(w http.ResponseWriter, r *http.Request, start time.Time, canonicalPath, ruleID string, decodedBody any) bool {
	names := s.namedVolumes(decodedBody)
	if len(names) == 0 {
		return true
	}
	name, err := s.deviceVolume(r.Context(), names)
	if err != nil {
		s.audit(AuditEntry{
			Ts:       start,
			CID:      s.cfg.CID,
			Channel:  s.cfg.ChannelID,
			Method:   r.Method,
			Path:     canonicalPath,
			Decision: "deny",
			RuleID:   "named-volume-device",
			Reason:   err.Error(),
			Latency:  s.cfg.Now().Sub(start),
		})
		http.Error(w, err.Error(), http.StatusForbidden)
		return false
	}
	if name == "" {
		return true
	}
	return s.runApprovalFlow(w, r, start, canonicalPath,
		"docker-body",
		ruleID, "named-volume-device",
		"container mounts volume "+name+", backed by a host device or path",
		"docker:POST:body:named-volume-device:"+name,
		decodedBody,
	)
}
