package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/container"
)

// restoreFirstTabWait is how long a restore waits for the first saved tab to
// load. It loads into the tab the browser came up with, which every action is
// about to act on; past this the load goes on in the background.
const restoreFirstTabWait = 3 * time.Second

// savedTab is one tab a channel had open when the daemon stopped.
type savedTab struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Active bool   `json:"active,omitempty"`
}

// savedTabsPath is where a channel's tabs wait for the next daemon. Empty when
// the daemon has no loop dir to keep them in, or the channel id leaves no
// file name.
func (s *browserService) savedTabsPath(channelID string) string {
	name := container.SanitizeName(channelID)
	if s.loopDir == "" || name == "" || strings.Contains(name, "..") {
		return ""
	}
	return filepath.Join(s.loopDir, "browser-tabs", name+".json")
}

// agentTabs narrows a browser's tab list to the tabs the channel's browser
// shows, in the order it shows them, marking the active one.
func agentTabs(mgr *browser.CDPManager, tabs []browser.TabInfo) []browser.TabInfo {
	if mgr == nil {
		return tabs
	}
	var shown []browser.TabInfo
	for _, t := range tabs {
		if mgr.IsTrackedTab(t.TargetID) {
			shown = append(shown, t)
		}
	}
	shown = mgr.OrderTabs(shown)
	active := mgr.ActiveTargetID()
	for i := range shown {
		shown[i].Active = shown[i].TargetID == active
	}
	return shown
}

// saveTabs writes down the tabs of every Docker browser, so the next daemon
// can open them again: stopping the daemon stops the sidecars, and Chrome
// comes back on a blank tab.
func (s *browserService) saveTabs(ctx context.Context) {
	s.cdpManagersMu.Lock()
	mgrs := make(map[string]*browser.CDPManager)
	for key, mgr := range s.cdpManagers {
		if channelID, ok := strings.CutSuffix(key, "|docker"); ok {
			mgrs[channelID] = mgr
		}
	}
	s.cdpManagersMu.Unlock()

	for channelID, mgr := range mgrs {
		s.saveChannelTabs(ctx, channelID, mgr)
	}
}

func (s *browserService) saveChannelTabs(ctx context.Context, channelID string, mgr *browser.CDPManager) {
	path := s.savedTabsPath(channelID)
	if path == "" || !hasLiveClient(mgr) {
		return
	}
	tabs, err := mgr.ActiveClient().ListTabs(ctx)
	if err != nil {
		s.deps.logger.Warn("browser: listing tabs to save failed", "channel_id", channelID, "error", err)
		return
	}
	var saved []savedTab
	for _, t := range agentTabs(mgr, tabs) {
		if t.URL == "" || t.URL == "about:blank" {
			continue
		}
		saved = append(saved, savedTab{URL: t.URL, Title: t.Title, Active: t.Active})
	}
	if len(saved) == 0 {
		// Nothing worth opening again; an older list must not come back either.
		_ = os.Remove(path)
		return
	}
	data, _ := json.Marshal(saved) // strings and bools: never fails
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.deps.logger.Warn("browser: saving tabs failed", "channel_id", channelID, "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		s.deps.logger.Warn("browser: saving tabs failed", "channel_id", channelID, "error", err)
		return
	}
	s.deps.logger.Info("browser: saved tabs", "channel_id", channelID, "tabs", len(saved))
}

// takeSavedTabs reads a channel's saved tabs and deletes them, so they are
// opened once, whatever comes of it. Serialized, so two callers connecting at
// once can't both get them.
func (s *browserService) takeSavedTabs(channelID string) []savedTab {
	path := s.savedTabsPath(channelID)
	if path == "" {
		return nil
	}
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.deps.logger.Warn("browser: reading saved tabs failed", "channel_id", channelID, "error", err)
		}
		return nil
	}
	_ = os.Remove(path)
	var saved []savedTab
	if err := json.Unmarshal(data, &saved); err != nil {
		s.deps.logger.Warn("browser: saved tabs are unreadable", "channel_id", channelID, "error", err)
		return nil
	}
	return saved
}

// restoreTabs opens the tabs a channel had when the daemon last stopped, on a
// browser that has just connected, and returns the client for the tab that
// was active then.
//
// A browser that already shows a page kept its tabs (the daemon died without
// stopping it), and is left as it is.
func (s *browserService) restoreTabs(ctx context.Context, channelID string, mgr *browser.CDPManager, client browser.CDPSession) browser.CDPSession {
	saved := s.takeSavedTabs(channelID)
	if len(saved) == 0 {
		return client
	}
	tabs, err := client.ListTabs(ctx)
	if err != nil {
		s.deps.logger.Warn("browser: not restoring tabs, listing failed", "channel_id", channelID, "error", err)
		return client
	}
	for _, t := range tabs {
		if t.URL != "" && t.URL != "about:blank" {
			s.deps.logger.Info("browser: not restoring tabs, the browser kept its own", "channel_id", channelID)
			return client
		}
	}

	activeID := ""
	for i, t := range saved {
		if s.carriesOwnerToken(t.URL) {
			continue
		}
		tid := client.TargetID()
		if i == 0 {
			// Into the tab the browser came up with, rather than a new one
			// next to a blank tab nobody opened.
			navCtx, cancel := context.WithTimeout(ctx, restoreFirstTabWait)
			err := client.Navigate(navCtx, t.URL)
			cancel()
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				s.deps.logger.Warn("browser: restoring a tab failed", "channel_id", channelID, "url", t.URL, "error", err)
			}
		} else {
			tid, err = client.NewTab(ctx, t.URL)
			if err != nil {
				s.deps.logger.Warn("browser: restoring a tab failed", "channel_id", channelID, "url", t.URL, "error", err)
				continue
			}
			mgr.TrackTab(tid)
			mgr.NotifyTabAdded(browser.TabInfo{TargetID: tid, URL: t.URL, Title: t.Title})
		}
		if t.Active || activeID == "" {
			activeID = tid
		}
	}
	if activeID == "" {
		return client
	}
	s.deps.logger.Info("browser: restored tabs", "channel_id", channelID, "tabs", len(saved))

	// A new tab comes to the front, so the one that was active is brought
	// back, as switch_tab would.
	if err := client.SwitchTab(ctx, activeID); err != nil {
		s.deps.logger.Warn("browser: bringing the restored active tab forward failed", "channel_id", channelID, "error", err)
	}
	mgr.SwitchActive(activeID)
	mgr.NotifyTargetSwitch(activeID)
	synced, err := s.syncActiveClient(ctx, mgr, client)
	if err != nil {
		s.deps.logger.Warn("browser: attaching to the restored active tab failed", "channel_id", channelID, "error", err)
		return client
	}
	return synced
}
