//go:build integration

package pluginsdktest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// UIIntegrationSuite runs CheckUI in a real headless Chrome.
type UIIntegrationSuite struct {
	suite.Suite
	ctx context.Context
}

func TestUIIntegrationSuite(t *testing.T) {
	suite.Run(t, new(UIIntegrationSuite))
}

func (s *UIIntegrationSuite) SetupTest() {
	var cancel context.CancelFunc
	s.ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
	s.T().Cleanup(cancel)
}

func (s *UIIntegrationSuite) TestGood() {
	var got []string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, r.Method+" "+r.URL.String()+" "+r.Header.Get("Content-Type")+" "+body["from"].(string))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":3}`))
	})
	problems, err := CheckUI(s.ctx, "testdata/ui/good", UIOptions{API: api, CardData: map[string]any{"summary": map[string]any{"total": 7}}})
	require.NoError(s.T(), err)
	require.Empty(s.T(), problems)
	require.Equal(s.T(), []string{"POST /stats?n=1 application/json main"}, got)
}

func (s *UIIntegrationSuite) TestBad() {
	problems, err := CheckUI(s.ctx, "testdata/ui/bad", UIOptions{Timeout: 3 * time.Second})
	require.NoError(s.T(), err)
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.String())
	}
	s.T().Log(strings.Join(lines, "\n"))
	has := func(page string, kind UIProblemKind, msg string) {
		for _, p := range problems {
			if p.Page == page && p.Kind == kind && strings.Contains(p.Message, msg) {
				return
			}
		}
		s.Failf("missing problem", "%s: %s: ...%s... in\n%s", page, kind, msg, strings.Join(lines, "\n"))
	}
	has("panel inline", UIProblemCSP, "script-src")
	has("panel inline", UIProblemBridge, "didn't connect")
	has("panel remote", UIProblemCSP, "https://example.com/data.json")
	has("panel remote", UIProblemError, "unhandled rejection: Error: nobody waits")
	has("panel remote", UIProblemError, "Failed to fetch")
	has("panel throws", UIProblemError, "boom")
	has("panel throws", UIProblemLoad, "ui/gone.js")
	has("panel throws", UIProblemBridge, "ui.open_file: the plugin wasn't approved for ui.open_file")
	has("panel missing", UIProblemLoad, "ui/missing.html")
	for _, p := range problems {
		if p.Page == "panel remote" || p.Page == "panel throws" {
			require.NotContains(s.T(), p.Message, "didn't connect", p.String())
		}
	}
}

func (s *UIIntegrationSuite) TestNoChrome() {
	_, err := CheckUI(s.ctx, "testdata/ui/good", UIOptions{Browser: Chrome{Options: chromeAt("/nonexistent/chrome")}})
	require.ErrorContains(s.T(), err, "no Chrome or Chromium found")
}
