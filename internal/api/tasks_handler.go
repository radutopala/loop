package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
)

// taskMutationStatus maps an AddTask/EditTask error to an HTTP status: 400 for
// an invalid user-supplied schedule or type, 500 for genuine server faults.
func taskMutationStatus(err error) int {
	if errors.Is(err, scheduler.ErrInvalidSchedule) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// resolveTaskChannelID walks up from deeply nested threads to the nearest
// channel that is either a top-level channel or a direct child of one.
// This ensures tasks are always listed/created at the correct level.
func (s *Server) resolveTaskChannelID(ctx context.Context, channelID string) string {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil || ch.ParentID == "" {
		return channelID
	}
	parent, err := s.store.GetChannel(ctx, ch.ParentID)
	if err != nil || parent == nil || parent.ParentID == "" {
		return channelID
	}
	return ch.ParentID
}

type createTaskRequest struct {
	ChannelID       string `json:"channel_id"`
	Schedule        string `json:"schedule"`
	Type            string `json:"type"`
	Prompt          string `json:"prompt"`
	TemplateName    string `json:"template_name,omitempty"`
	AutoDeleteSec   int    `json:"auto_delete_sec"`
	Worktree        bool   `json:"worktree"`
	OriginBranch    string `json:"origin_branch,omitempty"`
	UpdateBeforeRun bool   `json:"update_before_run"`
	WorkflowName    string `json:"workflow_name,omitempty"`
	WorkflowInputs  string `json:"workflow_inputs,omitempty"`
	BashScript      string `json:"bash_script,omitempty"`
}

type createTaskResponse struct {
	ID int64 `json:"id"`
}

type updateTaskRequest struct {
	Enabled         *bool   `json:"enabled"`
	Schedule        *string `json:"schedule"`
	Type            *string `json:"type"`
	Prompt          *string `json:"prompt"`
	AutoDeleteSec   *int    `json:"auto_delete_sec"`
	Worktree        *bool   `json:"worktree"`
	OriginBranch    *string `json:"origin_branch"`
	UpdateBeforeRun *bool   `json:"update_before_run"`
	WorkflowName    *string `json:"workflow_name"`
	WorkflowInputs  *string `json:"workflow_inputs"`
	BashScript      *string `json:"bash_script"`
}

type taskResponse struct {
	ID              int64     `json:"id"`
	ChannelID       string    `json:"channel_id"`
	Schedule        string    `json:"schedule"`
	Type            string    `json:"type"`
	Prompt          string    `json:"prompt"`
	Enabled         bool      `json:"enabled"`
	NextRunAt       time.Time `json:"next_run_at"`
	TemplateName    string    `json:"template_name,omitempty"`
	AutoDeleteSec   int       `json:"auto_delete_sec"`
	Worktree        bool      `json:"worktree"`
	OriginBranch    string    `json:"origin_branch,omitempty"`
	UpdateBeforeRun bool      `json:"update_before_run"`
	Running         bool      `json:"running"`
	ThreadID        string    `json:"thread_id,omitempty"`
	ChannelName     string    `json:"channel_name,omitempty"`
	DirPath         string    `json:"dir_path,omitempty"`
	ChannelWorktree bool      `json:"channel_worktree,omitempty"`
	WorkflowName    string    `json:"workflow_name,omitempty"`
	WorkflowInputs  string    `json:"workflow_inputs,omitempty"`
	BashScript      string    `json:"bash_script,omitempty"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	task := &db.ScheduledTask{
		ChannelID:       s.resolveTaskChannelID(r.Context(), req.ChannelID),
		Schedule:        req.Schedule,
		Type:            db.TaskType(req.Type),
		Prompt:          req.Prompt,
		Enabled:         true,
		TemplateName:    req.TemplateName,
		AutoDeleteSec:   req.AutoDeleteSec,
		Worktree:        req.Worktree,
		OriginBranch:    req.OriginBranch,
		UpdateBeforeRun: req.UpdateBeforeRun,
		WorkflowName:    req.WorkflowName,
		WorkflowInputs:  req.WorkflowInputs,
		BashScript:      req.BashScript,
	}

	id, err := s.scheduler.AddTask(r.Context(), task)
	if err != nil {
		http.Error(w, err.Error(), taskMutationStatus(err))
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskCreated(events.TaskEventData{TaskID: id, ChannelID: task.ChannelID})
	}

	writeHTTPJSON(w, http.StatusCreated, createTaskResponse{ID: id}, s.logger)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")

	var tasks []*db.ScheduledTask
	var err error
	if channelID == "" {
		tasks, err = s.store.ListAllScheduledTasks(r.Context())
	} else {
		tasks, err = s.scheduler.ListTasks(r.Context(), s.resolveTaskChannelID(r.Context(), channelID))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// For the global listing, enrich with channel info and filter by platform.
	var channelMap map[string]*db.Channel
	platform := r.URL.Query().Get("platform")
	if channelID == "" {
		channels, chErr := s.store.ListChannels(r.Context())
		if chErr == nil {
			channelMap = make(map[string]*db.Channel, len(channels))
			for _, ch := range channels {
				channelMap[ch.ChannelID] = ch
			}
		}
	}

	resp := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		tr := toTaskResponse(t)
		if channelMap != nil {
			ch := channelMap[t.ChannelID]
			if ch != nil {
				if platform != "" && string(ch.Platform) != platform {
					continue
				}
				tr.ChannelName = ch.Name
				tr.DirPath = ch.DirPath
				tr.ChannelWorktree = ch.Worktree
			}
		}
		resp = append(resp, tr)
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	task, err := s.scheduler.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	writeHTTPJSON(w, http.StatusOK, toTaskResponse(task), s.logger)
}

func toTaskResponse(t *db.ScheduledTask) taskResponse {
	return taskResponse{
		ID:              t.ID,
		ChannelID:       t.ChannelID,
		Schedule:        t.Schedule,
		Type:            string(t.Type),
		Prompt:          t.Prompt,
		Enabled:         t.Enabled,
		NextRunAt:       t.NextRunAt,
		TemplateName:    t.TemplateName,
		AutoDeleteSec:   t.AutoDeleteSec,
		Worktree:        t.Worktree,
		OriginBranch:    t.OriginBranch,
		UpdateBeforeRun: t.UpdateBeforeRun,
		Running:         t.Running,
		ThreadID:        t.ThreadID,
		WorkflowName:    t.WorkflowName,
		WorkflowInputs:  t.WorkflowInputs,
		BashScript:      t.BashScript,
	}
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	if err := s.scheduler.RemoveTask(r.Context(), taskID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskDeleted(events.TaskEventData{TaskID: taskID})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	var req updateTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Enabled == nil && req.Schedule == nil && req.Type == nil && req.Prompt == nil && req.AutoDeleteSec == nil && req.Worktree == nil && req.OriginBranch == nil && req.UpdateBeforeRun == nil && req.WorkflowName == nil && req.WorkflowInputs == nil && req.BashScript == nil {
		http.Error(w, "at least one field is required", http.StatusBadRequest)
		return
	}

	if req.Enabled != nil {
		if err := s.scheduler.SetTaskEnabled(r.Context(), taskID, *req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if req.Schedule != nil || req.Type != nil || req.Prompt != nil || req.AutoDeleteSec != nil || req.Worktree != nil || req.OriginBranch != nil || req.UpdateBeforeRun != nil || req.WorkflowName != nil || req.WorkflowInputs != nil || req.BashScript != nil {
		if err := s.scheduler.EditTask(r.Context(), taskID, req.Schedule, req.Type, req.Prompt, req.AutoDeleteSec, req.Worktree, req.OriginBranch, req.UpdateBeforeRun, req.WorkflowName, req.WorkflowInputs, req.BashScript); err != nil {
			http.Error(w, err.Error(), taskMutationStatus(err))
			return
		}
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskUpdated(events.TaskEventData{TaskID: taskID})
	}

	w.WriteHeader(http.StatusOK)
}

type moveTaskRequest struct {
	ChannelID string `json:"channel_id"`
}

// handleMoveTask re-homes a task under another channel or thread of the
// same project, keeping all of its settings. On the local platform its
// thread moves along; other platforms own their threads, so the task starts
// a fresh one on its next run. A thread that shares its parent's directory
// (not the task's own worktree) follows the new parent's, taking its
// session transcript along so the next run resumes it.
func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}
	var req moveTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	task, err := s.scheduler.GetTask(ctx, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	targetID := s.resolveTaskChannelID(ctx, req.ChannelID)
	if targetID == task.ChannelID {
		w.WriteHeader(http.StatusOK)
		return
	}
	if targetID == task.ThreadID {
		http.Error(w, "a task can't move into its own thread", http.StatusBadRequest)
		return
	}
	target, err := s.store.GetChannel(ctx, targetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	from, err := s.store.GetChannel(ctx, task.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if from == nil {
		http.Error(w, "task channel not found", http.StatusNotFound)
		return
	}
	// A task stays in its project: its worktree, origin branch, templates
	// and workflows all belong to the source repo.
	if rootChannelID(from) != rootChannelID(target) {
		http.Error(w, "a task can't move to another project", http.StatusBadRequest)
		return
	}

	move := db.TaskMove{TaskID: taskID, ChannelID: targetID, GuildID: target.GuildID}
	if task.ThreadID != "" && target.Platform == types.PlatformLocal {
		thread, err := s.store.GetChannel(ctx, task.ThreadID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if thread != nil {
			move.ThreadID = thread.ChannelID
			move.ThreadDirPath = s.movedThreadDir(thread, target)
		}
	}

	if err := s.store.MoveScheduledTask(ctx, move); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrTaskRunning) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskUpdated(events.TaskEventData{TaskID: taskID, ChannelID: targetID})
		if move.ThreadID != "" {
			// Reloads the sidebar, which now lists the thread under target.
			s.eventsHub.BroadcastChannelCreated(targetID, move.ThreadID)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// rootChannelID is the root channel of ch, a root channel or a depth-1
// thread, the only levels a task belongs to.
func rootChannelID(ch *db.Channel) string {
	if ch.ParentID == "" {
		return ch.ChannelID
	}
	return ch.ParentID
}

// movedThreadDir is the directory of a task's thread once it moves under
// target: its own worktree stays, a shared directory becomes target's.
func (s *Server) movedThreadDir(thread, target *db.Channel) string {
	if thread.Worktree || thread.DirPath == target.DirPath {
		return thread.DirPath
	}
	if thread.SessionID != "" {
		if err := s.copySessionFile(thread.DirPath, target.DirPath, thread.SessionID); err != nil {
			// The next run finds no transcript and starts a fresh session.
			s.logger.Warn("moving task thread session", "thread_id", thread.ChannelID, "error", err)
		}
	}
	return target.DirPath
}

func (s *Server) handleListTaskRuns(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	runs, err := s.store.ListTaskRunLogs(r.Context(), taskID, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, runs, s.logger)
}

func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	task, err := s.scheduler.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if task.Running {
		http.Error(w, "task is already running", http.StatusConflict)
		return
	}

	go func() {
		ctx := context.Background()
		if err := s.scheduler.RunNow(ctx, taskID); err != nil {
			s.logger.Error("run-now failed", "task_id", taskID, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}
