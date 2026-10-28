package mcpserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/stretchr/testify/require"
)

// statusAPI answers GET /api/channels with channels and each
// /api/channels/{id}/pr/status with prs[id], or a 404 without one.
func statusAPI(channels string, prs map[string]string) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/channels" {
			return jsonResponse(http.StatusOK, channels), nil
		}
		id, ok := strings.CutSuffix(strings.TrimPrefix(req.URL.Path, "/api/channels/"), "/pr/status")
		if !ok {
			return jsonResponse(http.StatusNotFound, "unexpected "+req.URL.Path), nil
		}
		if pr, ok := prs[id]; ok {
			return jsonResponse(http.StatusOK, pr), nil
		}
		return jsonResponse(http.StatusNotFound, "no PR/MR"), nil
	}
}

const statusChannels = `[
	{"channel_id":"root","name":"loop","dir_path":"/work/loop","branch":"main","commit":"6fb9594abcdef","subject":"Merge it","sync_base":"origin/main","upstream":"origin/main","container_running":true},
	{"channel_id":"test-channel","parent_id":"root","name":"feat-x","dir_path":"/work/loop/.worktrees/feat-x","branch":"feat/x","base_branch":"main","worktree":true,"commit":"abc1234","base_ahead":3,"base_behind":1,"sync_base":"main","agent_running":true,"locked":true,"pr_attention":"failed","unresolved_threads":2,"ticket_url":"https://example.com/T-1","description":"Fix the\nlogin","model_override":"opus","effort_override":"high"},
	{"channel_id":"child","parent_id":"test-channel","name":"notes","dir_path":"/work/loop/.worktrees/feat-x","branch":"HEAD","commit":"abc1234"},
	{"channel_id":"other","name":"elsewhere","dir_path":"/work/other"}
]`

func (s *MCPServerSuite) TestChannelStatusTree() {
	s.httpClient.doFunc = statusAPI(statusChannels, map[string]string{
		"root":         `{"present":true,"pr":{"kind":"pr","number":1,"checks":{"total":2}}}`,
		"test-channel": `{"present":true,"pr":{"kind":"pr","number":12,"url":"https://example.com/pull/12","checks":{"total":3,"failed":1},"failed":[{"name":"lint"}],"reason":"auto_fix_off"}}`,
	})

	text, isError := s.callTool("channel_status", map[string]any{})
	require.False(s.T(), isError, text)
	require.Equal(s.T(), `- loop [channel] (ID: root)
    path: /work/loop
    branch: main
    pr: #1 ✓ checks
    commit: 6fb9594 Merge it
    sync: even with origin/main · even with origin/main
    status: container running
  - feat-x [worktree thread] (ID: test-channel, this one)
      description: Fix the login
      ticket: https://example.com/T-1
      path: /work/loop/.worktrees/feat-x
      branch: feat/x (from main)
      pr: #12 ✗ 1 failing · https://example.com/pull/12
      ci: ✗ 1 failing (lint) Auto-fix is off for this thread.
      cmt: 2 unresolved review threads
      commit: abc1234
      sync: ↑3 ↓1 vs main
      model: opus · high
      status: agent running · locked
    - notes [thread] (ID: child)
        path: /work/loop/.worktrees/feat-x
        branch: detached
        commit: abc1234
`, text)
}

func (s *MCPServerSuite) TestChannelStatusRoots() {
	tests := []struct {
		name     string
		channels string
		input    map[string]any
		want     []string
		notWant  []string
	}{
		{
			name:     "a given channel's subtree",
			channels: statusChannels,
			input:    map[string]any{"channel_id": "test-channel"},
			want:     []string{"- feat-x [worktree thread]", "  - notes [thread]"},
			notWant:  []string{"loop [channel]", "elsewhere"},
		},
		{
			name:     "every tree when this agent's channel isn't listed",
			channels: `[{"channel_id":"a","name":"one"},{"channel_id":"b","name":"two"},{"channel_id":"c","parent_id":"a","name":"three"}]`,
			want:     []string{"- one [channel] (ID: a)\n  - three [thread] (ID: c)\n- two [channel] (ID: b)\n"},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = statusAPI(tt.channels, nil)
			text, isError := s.callTool("channel_status", tt.input)
			require.False(s.T(), isError, text)
			for _, w := range tt.want {
				require.Contains(s.T(), text, w)
			}
			for _, w := range tt.notWant {
				require.NotContains(s.T(), text, w)
			}
		})
	}
}

func (s *MCPServerSuite) TestChannelStatusEmpty() {
	s.httpClient.doFunc = statusAPI(`[]`, nil)
	text, isError := s.callTool("channel_status", map[string]any{})
	require.False(s.T(), isError)
	require.Equal(s.T(), "No channels found.", text)
}

func (s *MCPServerSuite) TestChannelStatusErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		input    map[string]any
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "db error"), nil
		}, nil, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, nil, "calling API"},
		{"unknown channel", statusAPI(statusChannels, nil), map[string]any{"channel_id": "nope"}, "channel nope not found"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("channel_status", tt.input)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestChannelStatusPRStatusMissing() {
	s.httpClient.doFunc = statusAPI(statusChannels, map[string]string{
		"root":         `{"present":false}`,
		"test-channel": `not json`,
	})
	text, isError := s.callTool("channel_status", map[string]any{})
	require.False(s.T(), isError, text)
	require.NotContains(s.T(), text, "pr: ")
	require.Contains(s.T(), text, "ci: failed\n")
}

func (s *MCPServerSuite) TestChannelStatusPRLines() {
	tests := []struct {
		name string
		ch   statusChannel
		pr   *statusPR
		want []string
	}{
		{
			name: "merge request",
			ch:   statusChannel{Branch: "b"},
			pr:   &statusPR{Kind: "mr", Number: 7, Merged: true},
			want: []string{"branch: b", "pr: !7 merged"},
		},
		{
			name: "fixing without a status",
			ch:   statusChannel{PRAttention: "fixing"},
			want: []string{"ci: agent fixing"},
		},
		{
			name: "fixing with nothing failing",
			ch:   statusChannel{PRAttention: "fixing"},
			pr:   &statusPR{Number: 1, Reason: "fix_sent"},
			want: []string{"pr: #1 no checks", "ci: agent fixing Auto-fix sent the failure to the agent."},
		},
		{
			name: "conflict with its base",
			ch:   statusChannel{PRAttention: "conflict"},
			pr:   &statusPR{Number: 1, MergeState: "conflict", BaseRef: "main"},
			want: []string{"pr: #1 ⚠ conflicts", "conflict: with main, yours to resolve"},
		},
		{
			name: "conflict without a status",
			ch:   statusChannel{PRAttention: "conflict", UnresolvedThreads: 1},
			want: []string{"conflict: with its base, yours to resolve", "cmt: 1 unresolved review thread"},
		},
		{
			name: "upstream only",
			ch:   statusChannel{Upstream: "origin/x", Ahead: 2},
			want: []string{"sync: ↑2 vs origin/x"},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, channelStatusLines(&tt.ch, tt.pr))
		})
	}
}

func (s *MCPServerSuite) TestChannelStatusPRChip() {
	tests := []struct {
		pr   statusPR
		want string
	}{
		{statusPR{Closed: true}, "closed"},
		{statusPR{Checks: statusChecks{Total: 4, Pending: 1}}, "◌ 3/4 running"},
		{statusPR{AutoMerge: true}, "merging when ready"},
		{statusPR{MergeArmed: "squash"}, "merging when ready"},
	}
	for _, tt := range tests {
		s.Run(tt.want, func() {
			require.Equal(s.T(), tt.want, prChip(&tt.pr))
		})
	}
}

func (s *MCPServerSuite) TestChannelStatusReasonText() {
	reasons := map[string]string{
		"not_agent_commit": "The head commit isn't one this thread's agent made, so auto-fix leaves it to you.",
		"auto_fix_off":     "Auto-fix is off for this thread.",
		"max_attempts":     "Auto-fix stopped after 3 attempts.",
		"no_new_commit":    "The last fix pushed no new commit.",
		"same_failure":     "The same failure came back after a fix.",
		"infra_rerun":      "The checks failed for runner reasons, so Loop re-ran them.",
		"infra_again":      "The checks failed for runner reasons again after a re-run.",
		"fix_sent":         "Auto-fix sent the failure to the agent.",
		"fix_asked":        "Fix it sent the failure to the agent.",
		"":                 "",
	}
	for reason, want := range reasons {
		s.Run(reason, func() {
			require.Equal(s.T(), want, prReasonText(&statusPR{Reason: reason, Attempts: 3}))
		})
	}
}
