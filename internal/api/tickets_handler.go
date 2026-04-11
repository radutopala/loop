package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	tk "github.com/radutopala/ticket/pkg/ticket"

	"github.com/radutopala/loop/internal/randutil"
)

// ── Types ──

type ticketResponse struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Type        string   `json:"type,omitempty"`
	Priority    int      `json:"priority"`
	Assignee    string   `json:"assignee,omitempty"`
	Tags        []string `json:"tags"`
	Deps        []string `json:"deps"`
	Links       []string `json:"links"`
	Parent      string   `json:"parent,omitempty"`
	ExternalRef string   `json:"external_ref,omitempty"`
	Design      string   `json:"design,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
	Created     string   `json:"created"`
}

func ticketToResponse(t *tk.Ticket) ticketResponse {
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	deps := t.Deps
	if deps == nil {
		deps = []string{}
	}
	links := t.Links
	if links == nil {
		links = []string{}
	}
	return ticketResponse{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		Type:        string(t.Type),
		Priority:    t.Priority,
		Assignee:    t.Assignee,
		Tags:        tags,
		Deps:        deps,
		Links:       links,
		Parent:      t.Parent,
		ExternalRef: t.ExternalRef,
		Design:      t.Design,
		Acceptance:  t.Acceptance,
		Created:     t.Created.Format(time.RFC3339),
	}
}

type createTicketRequest struct {
	Dir         string   `json:"dir"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	ExternalRef string   `json:"external_ref,omitempty"`
	Design      string   `json:"design,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
}

type updateTicketRequest struct {
	Dir         string   `json:"dir"`
	Status      *string  `json:"status,omitempty"`
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Type        *string  `json:"type,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Assignee    *string  `json:"assignee,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Deps        []string `json:"deps,omitempty"`
	Parent      *string  `json:"parent,omitempty"`
	ExternalRef *string  `json:"external_ref,omitempty"`
	Design      *string  `json:"design,omitempty"`
	Acceptance  *string  `json:"acceptance,omitempty"`
}

type assignTicketRequest struct {
	Dir       string `json:"dir"`
	ChannelID string `json:"channel_id"`
	Branch    string `json:"branch,omitempty"`
}

type assignTicketResponse struct {
	ThreadID     string `json:"thread_id"`
	WorktreePath string `json:"worktree_path"`
}

// generateID is the ticket ID generator, overridable for testing.
var generateID = tk.GenerateID

// TicketStore abstracts ticket CRUD operations so handlers can be tested
// with a mock store (e.g. to simulate write failures regardless of OS user).
type TicketStore interface {
	List() ([]*tk.Ticket, error)
	ResolveID(partial string) (string, error)
	Read(id string) (*tk.Ticket, error)
	EnsureDir() error
	Write(ticket *tk.Ticket) error
	Delete(id string) error
	AtomicClaim(id string) (*tk.Ticket, error)
}

// ── Helpers ──

// openTicketStore opens a ticket store for the given directory.
// dir must be the project root (parent of .tickets/).
func (s *Server) openTicketStore(w http.ResponseWriter, dir string) TicketStore {
	if dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return nil
	}
	if s.ticketStoreOpener != nil {
		return s.ticketStoreOpener(dir)
	}
	return tk.Open(dir)
}

// ── Handlers ──

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir := q.Get("dir")
	store := s.openTicketStore(w, dir)
	if store == nil {
		return
	}

	tickets, err := store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filter := tk.FilterOptions{
		Status:   q.Get("status"),
		Assignee: q.Get("assignee"),
		Tag:      q.Get("tag"),
		Type:     q.Get("type"),
	}
	filtered := tk.Filter(tickets, filter)
	tk.Sort(filtered, tk.SortOptions{SortBy: q.Get("sort"), Reverse: q.Get("reverse") == "true"})

	resp := make([]ticketResponse, 0, len(filtered))
	for _, t := range filtered {
		resp = append(resp, ticketToResponse(t))
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	store := s.openTicketStore(w, dir)
	if store == nil {
		return
	}

	id := r.PathValue("id")
	fullID, err := store.ResolveID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	ticket, err := store.Read(fullID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, ticketToResponse(ticket), s.logger)
}

func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	var req createTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	store := s.openTicketStore(w, req.Dir)
	if store == nil {
		return
	}

	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	if err := store.EnsureDir(); err != nil {
		http.Error(w, fmt.Sprintf("creating tickets dir: %s", err), http.StatusInternalServerError)
		return
	}

	id, err := generateID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ticketType := tk.TypeTask
	if req.Type != "" {
		parsed, err := tk.ParseType(req.Type)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ticketType = parsed
	}

	priority := tk.DefaultPriority
	if req.Priority != nil {
		priority = *req.Priority
		if err := tk.ValidatePriority(priority); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	ticket := &tk.Ticket{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		Status:      tk.StatusOpen,
		Type:        ticketType,
		Priority:    priority,
		Assignee:    req.Assignee,
		Tags:        req.Tags,
		Parent:      req.Parent,
		ExternalRef: req.ExternalRef,
		Design:      req.Design,
		Acceptance:  req.Acceptance,
		Created:     time.Now().UTC(),
	}

	if err := store.Write(ticket); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTicketEvent(EventTicketCreated, ticket.ID)
	}

	writeHTTPJSON(w, http.StatusCreated, ticketToResponse(ticket), s.logger)
}

func (s *Server) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	var req updateTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	store := s.openTicketStore(w, req.Dir)
	if store == nil {
		return
	}

	id := r.PathValue("id")
	fullID, err := store.ResolveID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	ticket, err := store.Read(fullID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Status != nil {
		newStatus, err := tk.ParseStatus(*req.Status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ticket.Status = newStatus
	}
	if req.Title != nil {
		if *req.Title == "" {
			http.Error(w, "title cannot be empty", http.StatusBadRequest)
			return
		}
		ticket.Title = *req.Title
	}
	if req.Description != nil {
		ticket.Description = *req.Description
	}
	if req.Type != nil {
		parsed, err := tk.ParseType(*req.Type)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ticket.Type = parsed
	}
	if req.Priority != nil {
		if err := tk.ValidatePriority(*req.Priority); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ticket.Priority = *req.Priority
	}
	if req.Assignee != nil {
		ticket.Assignee = *req.Assignee
	}
	if req.Tags != nil {
		ticket.Tags = req.Tags
	}
	if req.Deps != nil {
		ticket.Deps = req.Deps
	}
	if req.Parent != nil {
		ticket.Parent = *req.Parent
	}
	if req.ExternalRef != nil {
		ticket.ExternalRef = *req.ExternalRef
	}
	if req.Design != nil {
		ticket.Design = *req.Design
	}
	if req.Acceptance != nil {
		ticket.Acceptance = *req.Acceptance
	}

	if err := store.Write(ticket); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTicketEvent(EventTicketUpdated, ticket.ID)
	}

	writeHTTPJSON(w, http.StatusOK, ticketToResponse(ticket), s.logger)
}

func (s *Server) handleDeleteTicket(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	store := s.openTicketStore(w, dir)
	if store == nil {
		return
	}

	id := r.PathValue("id")
	fullID, err := store.ResolveID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := store.Delete(fullID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTicketEvent(EventTicketDeleted, fullID)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAssignTicket(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}

	var req assignTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	store := s.openTicketStore(w, req.Dir)
	if store == nil {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}

	id := r.PathValue("id")
	fullID, err := store.ResolveID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Atomically claim the ticket (open -> in_progress)
	ticket, err := store.AtomicClaim(fullID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	// Look up parent channel for dir_path and base branch
	parent, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}

	// Use the parent channel's current branch as the base ref for the worktree.
	// req.Branch overrides this if the caller wants a specific base.
	baseBranch := req.Branch
	if baseBranch == "" {
		baseBranch = gitBranch(r.Context(), parent.DirPath)
	}
	if baseBranch == "" {
		baseBranch = "HEAD"
	}

	// Create worktree
	name := "wt-" + randutil.HexID(4)
	result, err := s.worktreeCreator.Create(r.Context(), parent.DirPath, baseBranch, name, parent.SessionID)
	if err != nil {
		http.Error(w, fmt.Sprintf("creating worktree: %s", err), http.StatusInternalServerError)
		return
	}

	// Create thread
	threadName := fmt.Sprintf("%s (%s)", ticket.Title, ticket.ID)
	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, threadName, "", "")
	if err != nil {
		http.Error(w, fmt.Sprintf("creating thread: %s", err), http.StatusInternalServerError)
		return
	}

	// Update thread with worktree path
	ch, err := s.store.GetChannel(r.Context(), threadID)
	if err != nil || ch == nil {
		http.Error(w, "failed to get created thread", http.StatusInternalServerError)
		return
	}
	ch.DirPath = result.WorktreePath
	ch.Worktree = true
	if err := s.store.UpsertChannel(r.Context(), ch); err != nil {
		http.Error(w, fmt.Sprintf("updating thread: %s", err), http.StatusInternalServerError)
		return
	}

	// Record the assignee (thread name) in the ticket file (best-effort —
	// the critical operations above already succeeded).
	ticket.Assignee = threadName
	_ = store.Write(ticket)

	// Broadcast events
	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(req.ChannelID, threadID)
		s.eventsHub.BroadcastTicketEvent(EventTicketUpdated, ticket.ID)
	}

	// Auto-start agent with ticket description as prompt
	if s.msgHandler != nil && ticket.Description != "" {
		go s.msgHandler.HandleThreadCreated(context.Background(), threadID, "", ticket.Description)
	}

	writeHTTPJSON(w, http.StatusCreated, assignTicketResponse{
		ThreadID:     threadID,
		WorktreePath: result.WorktreePath,
	}, s.logger)
}
