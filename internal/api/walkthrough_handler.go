package api

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/db"
)

// Limits on a walkthrough an agent makes.
const (
	maxWalkthroughTitle  = 200
	maxWalkthroughSteps  = 50
	maxWalkthroughBody   = 8000
	maxWalkthroughAnchor = 4000
)

type createWalkthroughRequest struct {
	Title string                 `json:"title"`
	Steps []walkthroughStepInput `json:"steps"`
}

type walkthroughStepInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

// walkthroughChannel returns the channel at {id} and the channel its
// walkthroughs are kept under: a side chat's parent, or itself, so a tour
// one chat makes is there in the others. Fails the request when there's none.
func (s *Server) walkthroughChannel(w http.ResponseWriter, r *http.Request) (*db.Channel, string) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return nil, ""
	}
	group, _, err := s.agentGroup(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, ""
	}
	return ch, group
}

// handleCreateWalkthrough stores a walkthrough over the channel's files.
// Each step's lines must be in a text file under one of the channel's dirs;
// the text they hold now is kept as the step's anchor.
func (s *Server) handleCreateWalkthrough(w http.ResponseWriter, r *http.Request) {
	var req createWalkthroughRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, group := s.walkthroughChannel(w, r)
	if ch == nil {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	switch {
	case req.Title == "":
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	case len(req.Title) > maxWalkthroughTitle:
		http.Error(w, fmt.Sprintf("title is longer than %d bytes", maxWalkthroughTitle), http.StatusBadRequest)
		return
	case len(req.Steps) == 0 || len(req.Steps) > maxWalkthroughSteps:
		http.Error(w, fmt.Sprintf("a walkthrough has 1 to %d steps", maxWalkthroughSteps), http.StatusBadRequest)
		return
	}
	roots, err := s.allDirPaths(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	steps := make([]db.WalkthroughStep, 0, len(req.Steps))
	for i, in := range req.Steps {
		step, err := s.walkthroughStep(roots, in)
		if err != nil {
			http.Error(w, fmt.Sprintf("step %d: %v", i+1, err), http.StatusBadRequest)
			return
		}
		steps = append(steps, step)
	}
	wt, err := s.store.CreateWalkthrough(r.Context(), &db.Walkthrough{ChannelID: group, Title: req.Title, Steps: steps})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusCreated, wt, s.logger)
}

// walkthroughStep checks a step and returns it with its path as the editor
// opens it (relative to the first root, absolute under another) and its
// anchor.
func (s *Server) walkthroughStep(roots []string, in walkthroughStepInput) (db.WalkthroughStep, error) {
	in.Body = strings.TrimSpace(in.Body)
	if in.EndLine == 0 {
		in.EndLine = in.StartLine
	}
	switch {
	case in.Body == "":
		return db.WalkthroughStep{}, fmt.Errorf("body is required")
	case len(in.Body) > maxWalkthroughBody:
		return db.WalkthroughStep{}, fmt.Errorf("body is longer than %d bytes", maxWalkthroughBody)
	case len(in.Title) > maxWalkthroughTitle:
		return db.WalkthroughStep{}, fmt.Errorf("title is longer than %d bytes", maxWalkthroughTitle)
	case in.StartLine < 1 || in.EndLine < in.StartLine:
		return db.WalkthroughStep{}, fmt.Errorf("lines %d-%d aren't a range from line 1", in.StartLine, in.EndLine)
	}
	ok, idx, rel := s.resolveAndStat(roots, in.Path)
	if !ok {
		return db.WalkthroughStep{}, fmt.Errorf("no file %q in the channel's dirs", in.Path)
	}
	abs := filepath.Join(roots[idx], filepath.FromSlash(rel))
	if info, err := s.sys.Stat(abs); err != nil || info.Size() > maxFileSize {
		return db.WalkthroughStep{}, fmt.Errorf("%s can't be read or is too large", in.Path)
	}
	data, err := s.sys.ReadFileIn(roots[idx], abs)
	if err != nil {
		return db.WalkthroughStep{}, fmt.Errorf("reading %s: %w", in.Path, err)
	}
	if bytes.IndexByte(data[:min(len(data), 512)], 0) >= 0 {
		return db.WalkthroughStep{}, fmt.Errorf("%s is a binary file", in.Path)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if in.EndLine > len(lines) {
		return db.WalkthroughStep{}, fmt.Errorf("%s has %d lines, not %d", in.Path, len(lines), in.EndLine)
	}
	anchor := strings.Join(lines[in.StartLine-1:in.EndLine], "\n")
	if len(anchor) > maxWalkthroughAnchor {
		anchor = strings.ToValidUTF8(anchor[:maxWalkthroughAnchor], "")
	}
	path := rel
	if idx > 0 {
		path = abs
	}
	return db.WalkthroughStep{
		Path:      path,
		StartLine: in.StartLine,
		EndLine:   in.EndLine,
		Anchor:    anchor,
		Title:     strings.TrimSpace(in.Title),
		Body:      in.Body,
	}, nil
}

// handleListWalkthroughs lists the channel's walkthroughs, newest first.
func (s *Server) handleListWalkthroughs(w http.ResponseWriter, r *http.Request) {
	ch, group := s.walkthroughChannel(w, r)
	if ch == nil {
		return
	}
	list, err := s.store.ListWalkthroughs(r.Context(), group)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, append([]*db.Walkthrough{}, list...), s.logger)
}

// handleGetWalkthrough returns one of the channel's walkthroughs with its
// steps.
func (s *Server) handleGetWalkthrough(w http.ResponseWriter, r *http.Request) {
	ch, group := s.walkthroughChannel(w, r)
	if ch == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("wid"), 10, 64)
	if err != nil {
		http.Error(w, "invalid walkthrough id", http.StatusBadRequest)
		return
	}
	wt, err := s.store.GetWalkthrough(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wt == nil || wt.ChannelID != group {
		http.Error(w, "walkthrough not found", http.StatusNotFound)
		return
	}
	writeHTTPJSON(w, http.StatusOK, wt, s.logger)
}

// handleDeleteWalkthrough deletes one of the channel's walkthroughs.
func (s *Server) handleDeleteWalkthrough(w http.ResponseWriter, r *http.Request) {
	ch, group := s.walkthroughChannel(w, r)
	if ch == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("wid"), 10, 64)
	if err != nil {
		http.Error(w, "invalid walkthrough id", http.StatusBadRequest)
		return
	}
	ok, err := s.store.DeleteWalkthrough(r.Context(), group, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "walkthrough not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
