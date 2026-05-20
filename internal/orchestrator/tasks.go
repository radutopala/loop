package orchestrator

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/radutopala/loop/internal/events"
)

// taskRegistry maintains the per-channel cumulative state of agent tasks
// reconstructed from the Claude binary's TaskCreate / TaskUpdate tool calls
// in the streamed JSON. The disk under ~/.claude/tasks/<session-id>/ is the
// agent's source of truth; we mirror it in-memory so the FE can render the
// list without reading the filesystem.
//
// Lifecycle:
//   - applyCreate / applyUpdate mutate state, return the resulting filtered
//     list (deleted entries removed), or (nil, false) if the input could not
//     be parsed
//   - clear drops the per-channel entry, called when a run ends
//
// Concurrency: one mutex guards the whole map; per-run callbacks fire
// serially within a single agent turn so contention is minimal.
type taskRegistry struct {
	mu    sync.Mutex
	state map[string]map[string]events.TaskItem
}

func newTaskRegistry() *taskRegistry {
	return &taskRegistry{state: map[string]map[string]events.TaskItem{}}
}

// taskCreateIDPattern extracts the harness-assigned numeric id from the
// success line returned by TaskCreate (e.g. "Task #88 created successfully:
// ..."). The leading anchor avoids matching "#88" elsewhere in the output.
var taskCreateIDPattern = regexp.MustCompile(`^Task #(\d+) created`)

// taskCreateInput is the subset of fields TaskCreate accepts that we need
// to surface to the FE.
type taskCreateInput struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	ActiveForm  string `json:"activeForm"`
}

// taskUpdateInput mirrors the TaskUpdate harness schema. Pointer fields let
// us distinguish "omitted" from "set to empty" — only present keys overwrite.
type taskUpdateInput struct {
	TaskID       string    `json:"taskId"`
	Subject      *string   `json:"subject,omitempty"`
	Description  *string   `json:"description,omitempty"`
	ActiveForm   *string   `json:"activeForm,omitempty"`
	Status       *string   `json:"status,omitempty"`
	AddBlocks    *[]string `json:"addBlocks,omitempty"`
	AddBlockedBy *[]string `json:"addBlockedBy,omitempty"`
}

// applyCreate ingests a TaskCreate tool_use input plus its tool_result text.
// The result text carries the harness-assigned id; without it we cannot
// reference the new task in subsequent TaskUpdate calls, so the create is
// dropped and (nil, false) is returned.
func (r *taskRegistry) applyCreate(channelID, inputJSON, resultText string) ([]events.TaskItem, bool) {
	m := taskCreateIDPattern.FindStringSubmatch(resultText)
	if m == nil {
		return nil, false
	}
	id := m[1]
	var in taskCreateInput
	if err := json.Unmarshal([]byte(inputJSON), &in); err != nil {
		return nil, false
	}
	item := events.TaskItem{
		ID:          id,
		Subject:     in.Subject,
		Description: in.Description,
		ActiveForm:  in.ActiveForm,
		Status:      "pending",
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	channel := r.state[channelID]
	if channel == nil {
		channel = map[string]events.TaskItem{}
		r.state[channelID] = channel
	}
	channel[id] = item
	return r.snapshotLocked(channel), true
}

// applyUpdate ingests a TaskUpdate tool_use input. status="deleted" removes
// the task from state. Returns (nil, false) when the input is unparseable
// or names an unknown task.
func (r *taskRegistry) applyUpdate(channelID, inputJSON string) ([]events.TaskItem, bool) {
	var in taskUpdateInput
	if err := json.Unmarshal([]byte(inputJSON), &in); err != nil || in.TaskID == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	channel := r.state[channelID]
	if channel == nil {
		return nil, false
	}
	existing, ok := channel[in.TaskID]
	if !ok {
		return nil, false
	}
	if in.Status != nil && *in.Status == "deleted" {
		delete(channel, in.TaskID)
		return r.snapshotLocked(channel), true
	}
	if in.Subject != nil {
		existing.Subject = *in.Subject
	}
	if in.Description != nil {
		existing.Description = *in.Description
	}
	if in.ActiveForm != nil {
		existing.ActiveForm = *in.ActiveForm
	}
	if in.Status != nil {
		existing.Status = *in.Status
	}
	if in.AddBlocks != nil {
		existing.Blocks = mergeIDs(existing.Blocks, *in.AddBlocks)
	}
	if in.AddBlockedBy != nil {
		existing.BlockedBy = mergeIDs(existing.BlockedBy, *in.AddBlockedBy)
	}
	channel[in.TaskID] = existing
	return r.snapshotLocked(channel), true
}

// clear drops a channel's accumulated tasks. Called when a run ends so the
// next turn starts from a clean state — matches the FE's "clear on
// agent.status completed/error" behavior.
func (r *taskRegistry) clear(channelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.state, channelID)
}

// snapshotLocked returns the channel's tasks sorted by numeric id ascending.
// Caller must hold r.mu.
func (r *taskRegistry) snapshotLocked(channel map[string]events.TaskItem) []events.TaskItem {
	out := make([]events.TaskItem, 0, len(channel))
	for _, v := range channel {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := strconv.Atoi(out[i].ID)
		aj, _ := strconv.Atoi(out[j].ID)
		return ai < aj
	})
	return out
}

// mergeIDs appends ids from add that are not already in base, preserving order.
func mergeIDs(base, add []string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, id := range base {
		seen[id] = struct{}{}
	}
	for _, id := range add {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		base = append(base, id)
	}
	return base
}
