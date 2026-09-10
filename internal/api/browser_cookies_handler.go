package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/browsercookies"
)

// CookieReader reads cookies out of the browsers installed on the machine
// running loop. Implemented by browsercookies.Reader; an interface so tests
// inject a fake instead of a real Keychain and profile directory.
type CookieReader interface {
	Sources() []browsercookies.Source
	FindSource(id string) (browsercookies.Source, error)
	Cookies(src browsercookies.Source) ([]browsercookies.Cookie, error)
}

// cookieSourceResponse is one browser profile in the picker.
//
// It carries domains and counts only. Cookie names and values never leave
// this process: not in a response, not in a log line, not in an error
// string. Anything that changes that is a credential leak, not a debugging
// improvement.
type cookieSourceResponse struct {
	ID      string                         `json:"id"`
	Browser string                         `json:"browser"`
	Name    string                         `json:"name"`
	Domains []browsercookies.DomainSummary `json:"domains"`
	Error   string                         `json:"error,omitempty"`
}

type cookieImportRequest struct {
	ChannelID string   `json:"channel_id"`
	Source    string   `json:"source"`
	Domains   []string `json:"domains"`
}

type cookieImportResponse struct {
	Imported int `json:"imported"`
	Domains  int `json:"domains"`
}

// cookieClassifier builds a classifier seeded with the user's extra
// sensitive domains from config.
func (s *browserService) cookieClassifier() *browsercookies.Classifier {
	var extra []string
	if cfg := s.deps.configs.merged("", ""); cfg != nil {
		extra = cfg.Browser.CookieImport.SensitiveDomains
	}
	return browsercookies.NewClassifier(extra)
}

// handleBrowserCookieSources handles GET /api/browser/cookies/sources — the
// browser profiles on this machine and the cookie scopes each one holds.
//
// This is the call that triggers the macOS Keychain prompt, which is the
// consent gate for the whole feature: loop cannot read a single encrypted
// cookie until the user approves it at the OS level.
//
// A profile that fails to read is reported inline rather than failing the
// request, so one browser with a locked Keychain does not hide the others.
func (s *browserService) handleBrowserCookieSources(w http.ResponseWriter, r *http.Request) {
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" && s.modeFor(channelID) == "host" {
		http.Error(w, "host mode already uses your own browser profile, cookies included", http.StatusConflict)
		return
	}
	if s.cookieReader == nil {
		http.Error(w, "cookie import not configured", http.StatusServiceUnavailable)
		return
	}

	classifier := s.cookieClassifier()
	sources := s.cookieReader.Sources()
	out := make([]cookieSourceResponse, 0, len(sources))
	for _, src := range sources {
		resp := cookieSourceResponse{ID: src.ID(), Browser: src.Browser, Name: src.Name}
		cookies, err := s.cookieReader.Cookies(src)
		if err != nil {
			resp.Error = err.Error()
			s.deps.logger.Warn("cookie import: reading profile failed",
				"source", src.ID(), "error", err)
		} else {
			resp.Domains = browsercookies.Summarise(cookies, classifier)
		}
		out = append(out, resp)
	}
	writeHTTPJSON(w, http.StatusOK, out, s.deps.logger)
}

// handleBrowserCookieImport handles POST /api/browser/cookies/import — reads
// the chosen scopes out of the host browser and installs them into the
// channel's sidecar profile.
func (s *browserService) handleBrowserCookieImport(w http.ResponseWriter, r *http.Request) {
	var req cookieImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, "source required", http.StatusBadRequest)
		return
	}
	if len(req.Domains) == 0 {
		http.Error(w, "domains required", http.StatusBadRequest)
		return
	}
	if s.modeFor(req.ChannelID) == "host" {
		http.Error(w, "host mode already uses your own browser profile, cookies included", http.StatusConflict)
		return
	}
	if s.cookieReader == nil {
		http.Error(w, "cookie import not configured", http.StatusServiceUnavailable)
		return
	}

	imported, err := s.importCookies(r.Context(), req)
	if err != nil {
		s.deps.logger.Error("cookie import failed",
			"channel_id", req.ChannelID, "source", req.Source, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Domains, never names or values.
	s.deps.logger.Info("cookie import",
		"channel_id", req.ChannelID, "source", req.Source,
		"cookies", imported, "domains", len(req.Domains))
	writeHTTPJSON(w, http.StatusOK, cookieImportResponse{
		Imported: imported,
		Domains:  len(req.Domains),
	}, s.deps.logger)
}

// importCookies does the work behind the import endpoint: read, filter,
// convert, install.
func (s *browserService) importCookies(ctx context.Context, req cookieImportRequest) (int, error) {
	src, err := s.cookieReader.FindSource(req.Source)
	if err != nil {
		return 0, err
	}
	cookies, err := s.cookieReader.Cookies(src)
	if err != nil {
		return 0, err
	}

	selected := browsercookies.Filter(cookies, req.Domains)
	if len(selected) == 0 {
		return 0, fmt.Errorf("no cookies found for the selected sites")
	}

	// The sidecar has to exist before anything can be put into it;
	// getBrowserCDP starts it if it is not already running.
	cdpCl, err := s.getBrowserCDP(ctx, req.ChannelID)
	if err != nil {
		return 0, err
	}
	if err := cdpCl.SetCookies(ctx, toBrowserCookies(selected)); err != nil {
		return 0, err
	}
	return len(selected), nil
}

// toBrowserCookies converts the leaf package's cookies to the CDP layer's.
// The two types are deliberately separate so internal/browsercookies stays
// free of loop imports.
func toBrowserCookies(in []browsercookies.Cookie) []browser.Cookie {
	out := make([]browser.Cookie, len(in))
	for i, c := range in {
		out[i] = browser.Cookie{
			Domain:   c.Domain,
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			SameSite: c.SameSite,
		}
	}
	return out
}

// autoImportCookies replays the configured import into a sidecar that has
// just come up, when browser.cookie_import.auto is on.
//
// Every failure here is a warning, never an error: a browser the user has
// since uninstalled, or a Keychain prompt nobody is present to approve, must
// not stop their browser from starting.
func (s *browserService) autoImportCookies(ctx context.Context, channelID string, cdpCl browser.CDPSession) {
	if s.cookieReader == nil || cdpCl == nil {
		return
	}
	cfg := s.deps.configs.merged("", "")
	if cfg == nil {
		return
	}
	ci := cfg.Browser.CookieImport
	if !ci.Auto || ci.Source == "" || len(ci.Domains) == 0 {
		return
	}

	src, err := s.cookieReader.FindSource(ci.Source)
	if err != nil {
		s.deps.logger.Warn("cookie auto-import skipped", "source", ci.Source, "error", err)
		return
	}
	cookies, err := s.cookieReader.Cookies(src)
	if err != nil {
		s.deps.logger.Warn("cookie auto-import skipped", "source", ci.Source, "error", err)
		return
	}
	selected := browsercookies.Filter(cookies, ci.Domains)
	if len(selected) == 0 {
		s.deps.logger.Warn("cookie auto-import found nothing", "source", ci.Source, "domains", len(ci.Domains))
		return
	}
	if err := cdpCl.SetCookies(ctx, toBrowserCookies(selected)); err != nil {
		s.deps.logger.Warn("cookie auto-import failed", "channel_id", channelID, "error", err)
		return
	}
	s.deps.logger.Info("cookie auto-import",
		"channel_id", channelID, "source", ci.Source, "cookies", len(selected))
}
