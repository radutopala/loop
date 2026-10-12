package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/gitutil"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/types"
)

const (
	// pluginTreeMax caps how many files GET /channel/tree lists.
	pluginTreeMax = 50000
	// pluginRecentDays is the default and pluginRecentMaxDays the most days
	// GET /channel/git/recent looks back.
	pluginRecentDays    = 14
	pluginRecentMaxDays = 365
	// pluginRecentMaxCommits caps how many commits it reads.
	pluginRecentMaxCommits = 500
	// pluginEventMax caps the body of POST /events and POST /notify.
	pluginEventMax = 64 << 10
)

// pluginCaller returns the calling plugin and its channel when the plugin
// may run and the owner approved c for it; an empty c needs no
// capability. Otherwise it answers the request and returns false.
func (s *Server) pluginCaller(w http.ResponseWriter, r *http.Request, c plugin.Capability) (apiauth.Principal, *db.Channel, bool) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return apiauth.Principal{}, nil, false
	}
	p, _ := apiauth.PrincipalFrom(r.Context())
	if p.Kind != apiauth.KindPlugin {
		http.Error(w, "only a plugin calls the plugin API", http.StatusForbidden)
		return p, nil, false
	}
	if !pluginAllowed(w, s.plugins.Allows(r.Context(), p.PluginID, c)) {
		return p, nil, false
	}
	ch, err := s.store.GetChannel(r.Context(), p.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return p, nil, false
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return p, nil, false
	}
	return p, ch, true
}

// pluginAllowed answers the request and returns false when err, from the
// plugin service's Allows, refuses the plugin.
func pluginAllowed(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, install.ErrNotApproved), errors.Is(err, install.ErrNotInstalled):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, install.ErrNotRunnable):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		return true
	}
	return false
}

// pluginReadable returns whether the channel's agents may read a file of
// its dir, by its path relative to the dir: a plugin reads no more than
// they do. With the gate off, every file is readable.
func (s *Server) pluginReadable(r *http.Request, ch *db.Channel) (func(rel string) bool, error) {
	parent := worktreeRootDirPath(r.Context(), s.store, ch)
	cfg := s.configs.merged(ch.DirPath, parent)
	if cfg == nil {
		return nil, errors.New("loading the config failed")
	}
	if !cfg.Gates.Agentgate.Enabled {
		return func(string) bool { return true }, nil
	}
	policy, err := agentgate.CompilePolicy(cfg.Gates.Agentgate.DefaultDecision, nil, nil, container.GateFileRules(cfg, ch.DirPath, parent))
	if err != nil {
		return nil, fmt.Errorf("compiling the gate policy: %w", err)
	}
	return func(rel string) bool {
		return policy.MatchFile("read", filepath.Join(ch.DirPath, filepath.FromSlash(rel))).Decision == types.DecisionAllow
	}, nil
}

type pluginChannelResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DirPath  string `json:"dir_path"`
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	// Settings are the plugin's settings in the channel's project.
	Settings map[string]any `json:"settings"`
}

// handlePluginChannel describes the channel the calling plugin serves.
func (s *Server) handlePluginChannel(w http.ResponseWriter, r *http.Request) {
	p, ch, ok := s.pluginCaller(w, r, "")
	if !ok {
		return
	}
	_, m, err := s.plugins.UI(r.Context(), p.PluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	proj, err := s.pluginProject(r.Context(), ch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	settings, _ := proj.settings(m, p.PluginID)
	writeHTTPJSON(w, http.StatusOK, pluginChannelResponse{
		ID: ch.ChannelID, Name: ch.Name, DirPath: ch.DirPath, Platform: string(ch.Platform),
		Model: ch.ModelOverride, Effort: ch.EffortOverride, Settings: settings,
	}, s.logger)
}

type pluginFileResponse struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	TotalLines int    `json:"total_lines"`
}

// handlePluginFile reads a file of the channel's dir, or a range of its
// lines.
func (s *Server) handlePluginFile(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapFilesRead)
	if !ok {
		return
	}
	rel, err := cleanRelPath(r.URL.Query().Get("path"))
	if err == nil && slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), ".git") {
		err = errors.New("paths under .git are not readable")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	start, ok := parseQueryInt(w, r, "start", 0, math.MaxInt)
	if !ok {
		return
	}
	end, ok := parseQueryInt(w, r, "end", 0, math.MaxInt)
	if !ok {
		return
	}
	abs, err := s.validateFilePath(ch.DirPath, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	readable, err := s.pluginReadable(r, ch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !readable(rel) {
		http.Error(w, "the gate denies reading this path", http.StatusForbidden)
		return
	}
	info, err := s.sys.Stat(abs)
	switch {
	case err != nil:
		http.Error(w, "file not found", http.StatusNotFound)
		return
	case info.IsDir():
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	case info.Size() > maxFileSize:
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := s.sys.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	content, from, to, total, err := lineRange(string(data), start, end)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeHTTPJSON(w, http.StatusOK, pluginFileResponse{
		Path: filepath.ToSlash(rel), Content: content, Start: from, End: to, TotalLines: total,
	}, s.logger)
}

// lineRange returns lines start to end of content, 1-based and inclusive;
// 0 means from the first or to the last line. Without a range it returns
// content whole.
func lineRange(content string, start, end int) (string, int, int, int, error) {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	total := len(lines)
	if start == 0 && end == 0 {
		return content, min(1, total), total, total, nil
	}
	if start == 0 {
		start = 1
	}
	if end == 0 || end > total {
		end = total
	}
	if start > total || end < start {
		return "", 0, 0, total, fmt.Errorf("the file has %d lines", total)
	}
	return strings.Join(lines[start-1:end], "\n"), start, end, total, nil
}

type pluginTreeResponse struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
}

// handlePluginTree lists the files of the channel's dir: those git tracks,
// or every file outside .git when it isn't a git repo.
func (s *Server) handlePluginTree(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapFilesRead)
	if !ok {
		return
	}
	glob := r.URL.Query().Get("glob")
	if _, err := path.Match(glob, ""); err != nil {
		http.Error(w, "invalid glob", http.StatusBadRequest)
		return
	}
	readable, err := s.pluginReadable(r, ch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files, err := s.trackedFiles(r, ch.DirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := pluginTreeResponse{Files: []string{}}
	for _, f := range files {
		if glob != "" && !matchesGlob(glob, f) || !readable(f) {
			continue
		}
		if len(out.Files) == s.treeMax() {
			out.Truncated = true
			break
		}
		out.Files = append(out.Files, f)
	}
	writeHTTPJSON(w, http.StatusOK, out, s.logger)
}

// matchesGlob reports whether glob matches p or its base name.
func matchesGlob(glob, p string) bool {
	if ok, _ := path.Match(glob, p); ok {
		return true
	}
	ok, _ := path.Match(glob, path.Base(p))
	return ok
}

// treeMax is pluginTreeMax unless a test lowers it.
func (s *Server) treeMax() int {
	if s.treeMaxOverride > 0 {
		return s.treeMaxOverride
	}
	return pluginTreeMax
}

// trackedFiles lists the files git tracks in dir, or walks dir when it
// isn't a git repo.
func (s *Server) trackedFiles(r *http.Request, dir string) ([]string, error) {
	out, err := gitutil.Command(r.Context(), dir, "ls-files", "-z").Output()
	if err == nil {
		files := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if len(files) == 1 && files[0] == "" {
			return nil, nil
		}
		return files, nil
	}
	var files []string
	err = s.sys.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case !d.Type().IsRegular():
			return nil
		case len(files) > s.treeMax():
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	return files, nil
}

type pluginCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Time    string `json:"time"`
}

type pluginChangedFile struct {
	Path    string `json:"path"`
	Commits int    `json:"commits"`
}

type pluginRecentResponse struct {
	Commits []pluginCommit      `json:"commits"`
	Files   []pluginChangedFile `json:"files"`
}

// handlePluginGitRecent lists the commits of the last days in the
// channel's repo and the files they changed, most-changed first.
func (s *Server) handlePluginGitRecent(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapGitRead)
	if !ok {
		return
	}
	days, ok := parseQueryInt(w, r, "days", pluginRecentDays, pluginRecentMaxDays)
	if !ok {
		return
	}
	cmd := gitutil.Command(r.Context(), ch.DirPath, "log", "--no-merges",
		fmt.Sprintf("--since=%d.days.ago", days), fmt.Sprintf("-n%d", pluginRecentMaxCommits),
		"--format=%x1e%H%x1f%s%x1f%an%x1f%aI", "--name-only")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("git log: %s", strings.TrimSpace(stderr.String())), http.StatusConflict)
		return
	}
	writeHTTPJSON(w, http.StatusOK, parseRecentLog(string(out)), s.logger)
}

type pluginHeadResponse struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Time    string `json:"time"`
	// Branch is the branch checked out, "" when HEAD is detached.
	Branch string `json:"branch"`
}

// handlePluginGitHead describes the commit checked out in the channel's
// repo, and its branch.
func (s *Server) handlePluginGitHead(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapGitRead)
	if !ok {
		return
	}
	cmd := gitutil.Command(r.Context(), ch.DirPath, "log", "-1", "--format=%H%x1f%s%x1f%an%x1f%aI%x1f%D")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("git log: %s", strings.TrimSpace(stderr.String())), http.StatusConflict)
		return
	}
	writeHTTPJSON(w, http.StatusOK, parseHeadLog(string(out)), s.logger)
}

// parseHeadLog parses the git log handlePluginGitHead runs. Its last field
// lists the refs at HEAD, as in "HEAD -> main, tag: v1".
func parseHeadLog(out string) pluginHeadResponse {
	fields := strings.SplitN(strings.TrimSuffix(out, "\n"), "\x1f", 5)
	fields = append(fields, make([]string, 5-len(fields))...)
	res := pluginHeadResponse{SHA: fields[0], Subject: fields[1], Author: fields[2], Time: fields[3]}
	for ref := range strings.SplitSeq(fields[4], ", ") {
		if b, ok := strings.CutPrefix(ref, "HEAD -> "); ok {
			res.Branch = b
		}
	}
	return res
}

// parseRecentLog parses the git log handlePluginGitRecent runs.
func parseRecentLog(out string) pluginRecentResponse {
	res := pluginRecentResponse{Commits: []pluginCommit{}, Files: []pluginChangedFile{}}
	counts := map[string]int{}
	for rec := range strings.SplitSeq(out, "\x1e") {
		header, names, _ := strings.Cut(rec, "\n")
		fields := strings.Split(header, "\x1f")
		if len(fields) != 4 {
			continue
		}
		res.Commits = append(res.Commits, pluginCommit{SHA: fields[0], Subject: fields[1], Author: fields[2], Time: fields[3]})
		for name := range strings.SplitSeq(names, "\n") {
			if name = strings.TrimSpace(name); name != "" {
				counts[name]++
			}
		}
	}
	for p, n := range counts {
		res.Files = append(res.Files, pluginChangedFile{Path: p, Commits: n})
	}
	slices.SortFunc(res.Files, func(a, b pluginChangedFile) int {
		if a.Commits != b.Commits {
			return b.Commits - a.Commits
		}
		return strings.Compare(a.Path, b.Path)
	})
	return res
}

// handlePluginHotspots answers the latest quality scan's most complex
// functions of the channel, worst first.
func (s *Server) handlePluginHotspots(w http.ResponseWriter, r *http.Request) {
	_, ch, ok := s.pluginCaller(w, r, plugin.CapQualityRead)
	if !ok {
		return
	}
	q := r.Clone(r.Context())
	q.SetPathValue("id", ch.ChannelID)
	s.quality.handleQualityComplexity(w, q)
}

type pluginEventRequest struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// handlePluginEvent sends an event of the calling plugin to its panels in
// its channel.
func (s *Server) handlePluginEvent(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.pluginCaller(w, r, "")
	if !ok {
		return
	}
	var req pluginEventRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Type == "" {
		http.Error(w, "type is required", http.StatusBadRequest)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: "plugin.event", ChannelID: p.ChannelID, Data: map[string]any{
			"plugin_id": p.PluginID, "type": req.Type, "data": req.Data,
		}})
	}
	w.WriteHeader(http.StatusNoContent)
}

type pluginNotifyRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// handlePluginNotify keeps a notification from the calling plugin in its
// channel's notification drawer.
func (s *Server) handlePluginNotify(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.pluginCaller(w, r, plugin.CapUINotify)
	if !ok {
		return
	}
	var req pluginNotifyRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	s.recordPluginNotify(w, r, p.PluginID, p.ChannelID, req)
}

type pluginPageNotifyRequest struct {
	ChannelID string `json:"channel_id"`
	pluginNotifyRequest
}

// handlePluginPageNotify serves POST /api/plugins/{plugin}/notifications:
// a notification from one of the plugin's panel or card pages, which the
// app sends on its behalf. It's kept like one from the plugin's container.
func (s *Server) handlePluginPageNotify(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	var req pluginPageNotifyRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	ch, ok := s.pluginChannel(w, r, req.ChannelID)
	if !ok {
		return
	}
	pluginID := r.PathValue("plugin")
	shows, err := s.plugins.Shows(r.Context(), pluginID, plugin.CapUINotify, pluginRuntimeChannel(ch))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !shows {
		http.Error(w, "the plugin can't notify in this channel", http.StatusForbidden)
		return
	}
	s.recordPluginNotify(w, r, pluginID, ch.ChannelID, req.pluginNotifyRequest)
}

// recordPluginNotify keeps plugin pluginID's notification req in
// channelID's drawer, within the plugin's limit.
func (s *Server) recordPluginNotify(w http.ResponseWriter, r *http.Request, pluginID, channelID string, req pluginNotifyRequest) {
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if ok, wait := s.notifyLimit.allow(pluginID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		http.Error(w, "too many notifications; try again later", http.StatusTooManyRequests)
		return
	}
	if s.notifications != nil {
		n := &db.Notification{PluginID: pluginID, Kind: db.NotificationNotify, ChannelID: channelID, Title: req.Title, Body: req.Body}
		if err := s.notify(r.Context(), n); err != nil {
			s.logger.Warn("recording a plugin's notification", "plugin", pluginID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
