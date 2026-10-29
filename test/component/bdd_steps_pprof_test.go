//go:build component

package component

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

const (
	pprofBlockStart = "// bdd pprof start"
	pprofBlockEnd   = "// bdd pprof end"
)

// pprofSyncWait is how long the daemon may take to pick up a pprof config
// change: it re-reads the config every 5s.
const pprofSyncWait = 15 * time.Second

// setGlobalPprofConfig saves the global config through the API with its
// pprof block set to settings, replacing the block an earlier call added.
func (tc *TestContext) setGlobalPprofConfig(settings *godog.DocString) error {
	_, raw, err := tc.globalConfigFile()
	if err != nil {
		return err
	}
	raw = withoutPprofBlock(raw)
	open := strings.Index(raw, "{")
	if open < 0 {
		return fmt.Errorf("global config isn't an object: %q", raw)
	}
	return tc.saveGlobalConfig(raw[:open+1] + "\n" + pprofBlockStart + "\n\"pprof\": " + strings.TrimSpace(settings.Content) + ",\n" + pprofBlockEnd + "\n" + raw[open+1:])
}

// removeGlobalPprofConfig drops the block setGlobalPprofConfig added, so
// later scenarios see the global config without it.
func (tc *TestContext) removeGlobalPprofConfig() error {
	_, raw, err := tc.globalConfigFile()
	if err != nil {
		return err
	}
	return tc.saveGlobalConfig(withoutPprofBlock(raw))
}

// withoutPprofBlock returns raw without the block setGlobalPprofConfig added.
func withoutPprofBlock(raw string) string {
	if start, end := strings.Index(raw, pprofBlockStart), strings.Index(raw, pprofBlockEnd); start >= 0 && end > start {
		return raw[:start] + raw[end+len(pprofBlockEnd)+1:]
	}
	return raw
}

// waitForPprofServing waits until the daemon serves the pprof index on addr.
func (tc *TestContext) waitForPprofServing(addr string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	var last string
	for deadline := time.Now().Add(pprofSyncWait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		resp, err := client.Get("http://" + addr + "/debug/pprof/")
		if err != nil {
			last = err.Error()
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		last = resp.Status
	}
	return fmt.Errorf("pprof not served on %s: %s", addr, last)
}

// waitForPprofClosed waits until nothing listens on addr.
func (tc *TestContext) waitForPprofClosed(addr string) error {
	for deadline := time.Now().Add(pprofSyncWait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return nil
		}
		conn.Close()
	}
	return fmt.Errorf("%s still accepts connections", addr)
}
