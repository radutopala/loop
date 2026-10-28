package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prStatusFetches caps the PR/MR status requests channel_status has in
// flight at once.
const prStatusFetches = 8

type channelStatusInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"Optional channel or thread whose tree to show, with everything under it. Omit it for the tree this agent's channel or thread is in."`
}

// statusChannel is what channel_status reads of GET /api/channels.
type statusChannel struct {
	ChannelID         string `json:"channel_id"`
	Name              string `json:"name"`
	DirPath           string `json:"dir_path"`
	ParentID          string `json:"parent_id"`
	ContainerRunning  bool   `json:"container_running"`
	AgentRunning      bool   `json:"agent_running"`
	Branch            string `json:"branch"`
	Commit            string `json:"commit"`
	Subject           string `json:"subject"`
	Upstream          string `json:"upstream"`
	Ahead             int    `json:"ahead"`
	Behind            int    `json:"behind"`
	SyncBase          string `json:"sync_base"`
	BaseAhead         int    `json:"base_ahead"`
	BaseBehind        int    `json:"base_behind"`
	Worktree          bool   `json:"worktree"`
	BaseBranch        string `json:"base_branch"`
	Locked            bool   `json:"locked"`
	ModelOverride     string `json:"model_override"`
	EffortOverride    string `json:"effort_override"`
	Description       string `json:"description"`
	TicketURL         string `json:"ticket_url"`
	PRAttention       string `json:"pr_attention"`
	UnresolvedThreads int    `json:"unresolved_threads"`
}

// statusPR is what channel_status reads of GET /api/channels/{id}/pr/status.
type statusPR struct {
	Kind    string       `json:"kind"`
	Number  int          `json:"number"`
	URL     string       `json:"url"`
	BaseRef string       `json:"base_ref"`
	Checks  statusChecks `json:"checks"`
	Failed  []struct {
		Name string `json:"name"`
	} `json:"failed"`
	MergeState string `json:"merge_state"`
	AutoMerge  bool   `json:"auto_merge"`
	Merged     bool   `json:"merged"`
	Closed     bool   `json:"closed"`
	MergeArmed string `json:"merge_armed"`
	Attempts   int    `json:"attempts"`
	Reason     string `json:"reason"`
}

// statusChecks counts a PR/MR's checks.
type statusChecks struct {
	Total   int `json:"total"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

func (s *Server) handleChannelStatus(_ context.Context, _ *mcp.CallToolRequest, input channelStatusInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "channel_status", "channel_id", input.ChannelID)

	channels, errResult, err := doAPICall[[]statusChannel](s, "GET", s.apiURL+"/api/channels", http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	byID := map[string]*statusChannel{}
	children := map[string][]*statusChannel{}
	for i := range *channels {
		ch := &(*channels)[i]
		byID[ch.ChannelID] = ch
	}
	var roots []*statusChannel
	for i := range *channels {
		ch := &(*channels)[i]
		if byID[ch.ParentID] == nil {
			roots = append(roots, ch)
			continue
		}
		children[ch.ParentID] = append(children[ch.ParentID], ch)
	}

	switch {
	case input.ChannelID != "":
		root := byID[input.ChannelID]
		if root == nil {
			return errorResult(fmt.Sprintf("channel %s not found", input.ChannelID)), nil, nil
		}
		roots = []*statusChannel{root}
	case byID[s.channelID] != nil:
		root := byID[s.channelID]
		for byID[root.ParentID] != nil {
			root = byID[root.ParentID]
		}
		roots = []*statusChannel{root}
	}
	if len(roots) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No channels found."}},
		}, nil, nil
	}

	var rows []*statusChannel
	var walk func(ch *statusChannel)
	walk = func(ch *statusChannel) {
		rows = append(rows, ch)
		for _, c := range children[ch.ChannelID] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	prs := s.fetchPRStatuses(rows)

	var text strings.Builder
	var write func(ch *statusChannel, depth int)
	write = func(ch *statusChannel, depth int) {
		indent := strings.Repeat("  ", depth)
		kind := "channel"
		switch {
		case ch.Worktree:
			kind = "worktree thread"
		case ch.ParentID != "":
			kind = "thread"
		}
		current := ""
		if ch.ChannelID == s.channelID {
			current = ", this one"
		}
		fmt.Fprintf(&text, "%s- %s [%s] (ID: %s%s)\n", indent, ch.Name, kind, ch.ChannelID, current)
		for _, l := range channelStatusLines(ch, prs[ch.ChannelID]) {
			fmt.Fprintf(&text, "%s    %s\n", indent, l)
		}
		for _, c := range children[ch.ChannelID] {
			write(c, depth+1)
		}
	}
	for _, r := range roots {
		write(r, 0)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text.String()}},
	}, nil, nil
}

// fetchPRStatuses gets the PR/MR status of each row on a branch. A row whose
// status can't be had shows without one.
func (s *Server) fetchPRStatuses(rows []*statusChannel) map[string]*statusPR {
	type prStatusResponse struct {
		Present bool      `json:"present"`
		PR      *statusPR `json:"pr"`
	}
	out := map[string]*statusPR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, prStatusFetches)
	for _, ch := range rows {
		if ch.Branch == "" || ch.Branch == "HEAD" {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			resp, errResult, _ := doAPICall[prStatusResponse](s, "GET", s.apiURL+"/api/channels/"+ch.ChannelID+"/pr/status", http.StatusOK, nil)
			if errResult != nil {
				s.logger.Debug("channel_status: no PR/MR status", "channel_id", ch.ChannelID)
				return
			}
			if resp.Present && resp.PR != nil {
				mu.Lock()
				out[ch.ChannelID] = resp.PR
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

// channelStatusLines are what the desktop app's sidebar shows when hovering
// a row: its description, ticket, directory, branch, PR/MR, why its ci, cnf and cmt pills
// are lit, its commit, how far it is from its base and upstream, its
// model/effort overrides and whether its agent is running or it's locked.
func channelStatusLines(ch *statusChannel, pr *statusPR) []string {
	var lines []string
	if d := strings.Join(strings.Fields(ch.Description), " "); d != "" {
		lines = append(lines, "description: "+d)
	}
	if ch.TicketURL != "" {
		lines = append(lines, "ticket: "+ch.TicketURL)
	}
	if ch.DirPath != "" {
		lines = append(lines, "path: "+ch.DirPath)
	}
	commit := ch.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	switch {
	case ch.Branch != "" && ch.Branch != "HEAD":
		from := ""
		if ch.Worktree && ch.BaseBranch != "" {
			from = " (from " + ch.BaseBranch + ")"
		}
		lines = append(lines, "branch: "+ch.Branch+from)
	case commit != "":
		lines = append(lines, "branch: detached")
	}
	if pr != nil {
		ref := "#"
		if pr.Kind == "mr" {
			ref = "!"
		}
		lines = append(lines, strings.Join(nonEmpty(fmt.Sprintf("pr: %s%d %s", ref, pr.Number, prChip(pr)), pr.URL), " · "))
	}
	lines = append(lines, prAttentionLines(ch, pr)...)
	if commit != "" {
		lines = append(lines, strings.Join(nonEmpty("commit: "+commit, ch.Subject), " "))
	}
	var syncs []string
	if ch.SyncBase != "" {
		syncs = append(syncs, distance(ch.BaseAhead, ch.BaseBehind, ch.SyncBase))
	}
	if ch.Upstream != "" {
		syncs = append(syncs, distance(ch.Ahead, ch.Behind, ch.Upstream))
	}
	if len(syncs) > 0 {
		lines = append(lines, "sync: "+strings.Join(syncs, " · "))
	}
	if model := strings.Join(nonEmpty(ch.ModelOverride, ch.EffortOverride), " · "); model != "" {
		lines = append(lines, "model: "+model)
	}
	running := ""
	switch {
	case ch.AgentRunning:
		running = "agent running"
	case ch.ContainerRunning:
		running = "container running"
	}
	locked := ""
	if ch.Locked {
		locked = "locked"
	}
	if status := strings.Join(nonEmpty(running, locked), " · "); status != "" {
		lines = append(lines, "status: "+status)
	}
	return lines
}

// prAttentionLines say why a row's PR/MR pills are lit: its CI failure,
// merge conflict and unresolved review threads.
func prAttentionLines(ch *statusChannel, pr *statusPR) []string {
	var lines []string
	switch ch.PRAttention {
	case "failed", "fixing":
		fixing := ""
		if ch.PRAttention == "fixing" {
			fixing = "agent fixing"
		}
		value := fixing
		detail := ""
		if pr != nil {
			failing := ""
			if pr.Checks.Failed > 0 {
				failing = prChip(pr)
			}
			value = strings.Join(nonEmpty(failing, fixing), " · ")
			var names []string
			for _, f := range pr.Failed {
				names = append(names, f.Name)
			}
			if len(names) > 0 {
				detail = "(" + strings.Join(names, ", ") + ")"
			}
			detail = strings.Join(nonEmpty(detail, prReasonText(pr)), " ")
		}
		if value == "" {
			value = "failed"
		}
		lines = append(lines, strings.Join(nonEmpty("ci: "+value, detail), " "))
	case "conflict":
		base := "its base"
		if pr != nil && pr.BaseRef != "" {
			base = pr.BaseRef
		}
		lines = append(lines, "conflict: with "+base+", yours to resolve")
	}
	if n := ch.UnresolvedThreads; n > 0 {
		plural := "s"
		if n == 1 {
			plural = ""
		}
		lines = append(lines, fmt.Sprintf("cmt: %d unresolved review thread%s", n, plural))
	}
	return lines
}

// prChip is the PR/MR's state in a few words, as the Git panel's chip says.
func prChip(pr *statusPR) string {
	c := pr.Checks
	switch {
	case pr.Merged:
		return "merged"
	case pr.Closed:
		return "closed"
	case pr.MergeState == "conflict":
		return "⚠ conflicts"
	case c.Failed > 0:
		return fmt.Sprintf("✗ %d failing", c.Failed)
	case c.Pending > 0:
		return fmt.Sprintf("◌ %d/%d running", c.Total-c.Pending, c.Total)
	case pr.MergeArmed != "" || pr.AutoMerge:
		return "merging when ready"
	case c.Total > 0:
		return "✓ checks"
	default:
		return "no checks"
	}
}

// prReasonText says why the PR/MR watcher acted as it did on a failure.
func prReasonText(pr *statusPR) string {
	switch pr.Reason {
	case "not_agent_commit":
		return "The head commit isn't one this thread's agent made, so auto-fix leaves it to you."
	case "auto_fix_off":
		return "Auto-fix is off for this thread."
	case "max_attempts":
		return fmt.Sprintf("Auto-fix stopped after %d attempts.", pr.Attempts)
	case "no_new_commit":
		return "The last fix pushed no new commit."
	case "same_failure":
		return "The same failure came back after a fix."
	case "infra_rerun":
		return "The checks failed for runner reasons, so Loop re-ran them."
	case "infra_again":
		return "The checks failed for runner reasons again after a re-run."
	case "fix_sent":
		return "Auto-fix sent the failure to the agent."
	case "fix_asked":
		return "Fix it sent the failure to the agent."
	default:
		return ""
	}
}

// distance says how far a branch is from another: "↑3 ↓1 vs main" or
// "even with main".
func distance(ahead, behind int, other string) string {
	var arrows []string
	if ahead > 0 {
		arrows = append(arrows, fmt.Sprintf("↑%d", ahead))
	}
	if behind > 0 {
		arrows = append(arrows, fmt.Sprintf("↓%d", behind))
	}
	if len(arrows) == 0 {
		return "even with " + other
	}
	return strings.Join(arrows, " ") + " vs " + other
}

// nonEmpty is parts without the empty ones.
func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
