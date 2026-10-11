package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/plugin"
)

type NotifyLimitSuite struct {
	suite.Suite
}

func TestNotifyLimitSuite(t *testing.T) {
	suite.Run(t, new(NotifyLimitSuite))
}

func (s *NotifyLimitSuite) TestAllow() {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := &notifyLimit{now: func() time.Time { return now }}
	for range pluginNotifyBurst {
		ok, _ := l.allow("echo")
		require.True(s.T(), ok)
		now = now.Add(10 * time.Second)
	}
	// The window holds 0s..40s: the first leaves it at 60s.
	ok, wait := l.allow("echo")
	require.False(s.T(), ok)
	require.Equal(s.T(), 10*time.Second, wait)
	ok, _ = l.allow("other")
	require.True(s.T(), ok, "each plugin has its own limit")

	now = now.Add(10 * time.Second)
	ok, _ = l.allow("echo")
	require.True(s.T(), ok)
	ok, _ = l.allow("echo")
	require.False(s.T(), ok)
}

func (s *NotifyLimitSuite) TestAllowRealClock() {
	var l notifyLimit
	ok, wait := l.allow("echo")
	require.True(s.T(), ok)
	require.Zero(s.T(), wait)
}

func (s *ServerSuite) TestPluginNotifyLimited() {
	s.pluginAPIRoutes(plugin.CapUINotify, "/src/app")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.srv.notifyLimit.now = func() time.Time { return now }
	for range pluginNotifyBurst {
		require.Equal(s.T(), http.StatusNoContent, s.requestAs(echoPlugin, "POST", "/api/plugin/v1/notify", `{"title":"t"}`).Code)
	}
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/notify", `{"title":"t"}`)
	require.Equal(s.T(), http.StatusTooManyRequests, rec.Code)
	require.Equal(s.T(), "60", rec.Header().Get("Retry-After"))

	now = now.Add(pluginNotifyWindow)
	require.Equal(s.T(), http.StatusNoContent, s.requestAs(echoPlugin, "POST", "/api/plugin/v1/notify", `{"title":"t"}`).Code)
}
