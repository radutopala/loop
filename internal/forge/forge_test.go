package forge

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ForgeSuite struct {
	suite.Suite
}

func TestForgeSuite(t *testing.T) {
	suite.Run(t, new(ForgeSuite))
}

func (s *ForgeSuite) TestPickMethod() {
	tests := []struct {
		name       string
		configured string
		allowed    []string
		want       string
		wantErr    string
	}{
		{name: "configured and allowed", configured: "rebase", allowed: []string{"squash", "rebase"}, want: "rebase"},
		{name: "configured, not allowed", configured: "rebase", allowed: []string{"squash"}, wantErr: "doesn't allow rebase"},
		{name: "squash first", allowed: []string{"merge", "squash"}, want: "squash"},
		{name: "merge before rebase", allowed: []string{"rebase", "merge"}, want: "merge"},
		{name: "none", wantErr: "no merge method"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := PickMethod(tc.configured, MergeSettings{Methods: tc.allowed})
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *ForgeSuite) TestTail() {
	var lines []string
	for i := range 300 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := Tail(strings.Join(lines, "\n") + "\n")
	require.True(s.T(), strings.HasPrefix(got, "line 100\n"))
	require.True(s.T(), strings.HasSuffix(got, "line 299"))

	long := strings.Repeat(strings.Repeat("x", 199)+"\n", 150)
	got = Tail(long)
	require.LessOrEqual(s.T(), len(got), 16<<10)
	require.True(s.T(), strings.HasPrefix(got, "xxx"), "starts on a whole line")

	require.Equal(s.T(), "short", Tail("short"))
	oneLine := strings.Repeat("y", 20<<10)
	require.Len(s.T(), Tail(oneLine), 16<<10)
}

func (s *ForgeSuite) TestLogHash() {
	a := LogHash("2026-10-01T10:00:00.123Z FAIL TestX (0.12s)\n")
	b := LogHash("2026-10-02T11:30:00.456Z FAIL TestX (3.40s)\n")
	require.Equal(s.T(), a, b)
	require.NotEqual(s.T(), a, LogHash("FAIL TestY"))
	require.Len(s.T(), a, 16)
}

func (s *ForgeSuite) TestSettingsCache() {
	var c SettingsCache
	calls := 0
	fetch := func() (MergeSettings, error) {
		calls++
		return MergeSettings{Methods: []string{"merge"}}, nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := c.Get("k", now, fetch)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"merge"}, got.Methods)
	_, _ = c.Get("k", now.Add(9*time.Minute), fetch)
	require.Equal(s.T(), 1, calls, "cached")
	_, _ = c.Get("k", now.Add(11*time.Minute), fetch)
	require.Equal(s.T(), 2, calls, "stale")

	_, err = c.Get("other", now, func() (MergeSettings, error) { return MergeSettings{}, errors.New("boom") })
	require.ErrorContains(s.T(), err, "boom")
	_, _ = c.Get("other", now, fetch)
	require.Equal(s.T(), 3, calls, "errors aren't cached")
}

func (s *ForgeSuite) TestLabel() {
	require.Equal(s.T(), "ci / lint", FailedCheck{Group: "ci", Name: "lint"}.Label())
	require.Equal(s.T(), "lint", FailedCheck{Name: "lint"}.Label())
}
