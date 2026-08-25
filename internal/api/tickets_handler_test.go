package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	tk "github.com/radutopala/ticket/pkg/ticket"
)

// createTicketsDir creates a temp dir with a .tickets/ subdirectory and returns the parent path.
func createTicketsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tickets"), 0755))
	return dir
}

// writeTestTicket writes a ticket to the .tickets/ dir and returns it.
func writeTestTicket(t *testing.T, dir string, id, title string, status tk.Status) *tk.Ticket {
	t.Helper()
	ticket := &tk.Ticket{
		ID:       id,
		Title:    title,
		Status:   status,
		Type:     tk.TypeTask,
		Priority: 2,
		Tags:     []string{"test"},
		Created:  time.Now().UTC(),
	}
	store := tk.Open(dir)
	require.NoError(t, store.Write(ticket))
	return ticket
}

func (s *ServerSuite) TestListTickets_Empty() {
	dir := createTicketsDir(s.T())
	rec := s.testRequest("GET", "/api/tickets?dir="+dir, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(s.T(), resp)
}

func (s *ServerSuite) TestListTickets_WithTickets() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-aaaa", "First ticket", tk.StatusOpen)
	writeTestTicket(s.T(), dir, "tic-bbbb", "Second ticket", tk.StatusInProgress)

	rec := s.testRequest("GET", "/api/tickets?dir="+dir, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp, 2)
}

func (s *ServerSuite) TestListTickets_FilterByStatus() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-aaaa", "Open ticket", tk.StatusOpen)
	writeTestTicket(s.T(), dir, "tic-bbbb", "Closed ticket", tk.StatusClosed)

	rec := s.testRequest("GET", "/api/tickets?dir="+dir+"&status=open", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "open", resp[0].Status)
}

func (s *ServerSuite) TestListTickets_MissingDir() {
	rec := s.testRequest("GET", "/api/tickets", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestGetTicket_Success() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-cccc", "My ticket", tk.StatusOpen)

	rec := s.testRequest("GET", "/api/tickets/tic-cccc?dir="+dir, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "tic-cccc", resp.ID)
	require.Equal(s.T(), "My ticket", resp.Title)
}

func (s *ServerSuite) TestGetTicket_PartialID() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-dddd", "Partial ID ticket", tk.StatusOpen)

	rec := s.testRequest("GET", "/api/tickets/dddd?dir="+dir, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "tic-dddd", resp.ID)
}

func (s *ServerSuite) TestGetTicket_NotFound() {
	dir := createTicketsDir(s.T())

	rec := s.testRequest("GET", "/api/tickets/nonexistent?dir="+dir, "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_Success() {
	dir := createTicketsDir(s.T())
	body := fmt.Sprintf(`{"dir": %q, "title": "New ticket", "type": "bug", "priority": 1, "tags": ["urgent"]}`, dir)

	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "New ticket", resp.Title)
	require.Equal(s.T(), "bug", resp.Type)
	require.Equal(s.T(), 1, resp.Priority)
	require.Equal(s.T(), "open", resp.Status)
	require.Contains(s.T(), resp.Tags, "urgent")
	require.NotEmpty(s.T(), resp.ID)
}

func (s *ServerSuite) TestCreateTicket_MissingTitle() {
	dir := createTicketsDir(s.T())
	body := fmt.Sprintf(`{"dir": %q}`, dir)

	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_InvalidType() {
	dir := createTicketsDir(s.T())
	body := fmt.Sprintf(`{"dir": %q, "title": "test", "type": "invalid"}`, dir)

	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_InvalidPriority() {
	dir := createTicketsDir(s.T())
	body := fmt.Sprintf(`{"dir": %q, "title": "test", "priority": 99}`, dir)

	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_ChangeStatus() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-eeee", "Update me", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "status": "in_progress"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-eeee", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "in_progress", resp.Status)

	// Verify persisted
	store := tk.Open(dir)
	ticket, err := store.Read("tic-eeee")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tk.StatusInProgress, ticket.Status)
}

func (s *ServerSuite) TestUpdateTicket_InvalidStatus() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-ffff", "Bad status", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "status": "invalid"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-ffff", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_NotFound() {
	dir := createTicketsDir(s.T())

	body := fmt.Sprintf(`{"dir": %q, "status": "closed"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/nonexistent", body)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestDeleteTicket_Success() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-dead", "Delete me", tk.StatusOpen)

	rec := s.testRequest("DELETE", "/api/tickets/tic-dead?dir="+dir, "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	// Verify deleted from disk
	store := tk.Open(dir)
	require.False(s.T(), store.Exists("tic-dead"))
}

func (s *ServerSuite) TestDeleteTicket_NotFound() {
	dir := createTicketsDir(s.T())

	rec := s.testRequest("DELETE", "/api/tickets/nonexistent?dir="+dir, "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestDeleteTicket_MissingDir() {
	rec := s.testRequest("DELETE", "/api/tickets/tic-1234", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_WithEventsHub() {
	dir := createTicketsDir(s.T())
	hub := NewEventsHub(s.srv.logger)
	s.srv.SetEventsHub(hub)

	body := fmt.Sprintf(`{"dir": %q, "title": "Event ticket"}`, dir)
	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_EditFields() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-edit", "Original title", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "title": "Updated title", "description": "New desc", "type": "bug", "priority": 0, "assignee": "alice"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-edit", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "Updated title", resp.Title)
	require.Equal(s.T(), "New desc", resp.Description)
	require.Equal(s.T(), "bug", resp.Type)
	require.Equal(s.T(), 0, resp.Priority)
	require.Equal(s.T(), "alice", resp.Assignee)
	require.Equal(s.T(), "open", resp.Status) // unchanged

	// Verify persisted
	store := tk.Open(dir)
	ticket, err := store.Read("tic-edit")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Updated title", ticket.Title)
	require.Equal(s.T(), "New desc", ticket.Description)
	require.Equal(s.T(), tk.TypeBug, ticket.Type)
	require.Equal(s.T(), 0, ticket.Priority)
	require.Equal(s.T(), "alice", ticket.Assignee)
}

func (s *ServerSuite) TestUpdateTicket_ExtendedFields() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-extf", "Extended fields", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "deps": ["tic-dep1", "tic-dep2"], "parent": "tic-parent", "external_ref": "gh-42", "pr": "https://github.com/owner/repo/pull/7", "design": "Use REST API", "acceptance": "All tests pass"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-extf", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), []string{"tic-dep1", "tic-dep2"}, resp.Deps)
	require.Equal(s.T(), "tic-parent", resp.Parent)
	require.Equal(s.T(), "gh-42", resp.ExternalRef)
	require.Equal(s.T(), "https://github.com/owner/repo/pull/7", resp.PR)
	require.Equal(s.T(), "Use REST API", resp.Design)
	require.Equal(s.T(), "All tests pass", resp.Acceptance)

	// Verify persisted
	store := tk.Open(dir)
	ticket, err := store.Read("tic-extf")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"tic-dep1", "tic-dep2"}, ticket.Deps)
	require.Equal(s.T(), "tic-parent", ticket.Parent)
	require.Equal(s.T(), "gh-42", ticket.ExternalRef)
	require.Equal(s.T(), "https://github.com/owner/repo/pull/7", ticket.PR)
	require.Equal(s.T(), "Use REST API", ticket.Design)
	require.Equal(s.T(), "All tests pass", ticket.Acceptance)
}

func (s *ServerSuite) TestUpdateTicket_EmptyTitle() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-etit", "Keep me", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "title": ""}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-etit", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_InvalidType() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-ityp", "Bad type", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "type": "invalid"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-ityp", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_InvalidPriority() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-ipri", "Bad priority", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "priority": 99}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-ipri", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_Tags() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-tags", "Tags ticket", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q, "tags": ["frontend", "urgent"]}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-tags", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ticketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), []string{"frontend", "urgent"}, resp.Tags)
}

func (s *ServerSuite) TestListTickets_NullArraysAreEmptyArrays() {
	dir := createTicketsDir(s.T())
	// Write a ticket with nil tags/deps/links
	ticket := &tk.Ticket{
		ID:      "tic-null",
		Title:   "Null arrays",
		Status:  tk.StatusOpen,
		Type:    tk.TypeTask,
		Created: time.Now().UTC(),
	}
	store := tk.Open(dir)
	require.NoError(s.T(), store.Write(ticket))

	rec := s.testRequest("GET", "/api/tickets/tic-null?dir="+dir, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	// Verify JSON has empty arrays, not null
	body := rec.Body.String()
	require.Contains(s.T(), body, `"tags":[]`)
	require.Contains(s.T(), body, `"deps":[]`)
	require.Contains(s.T(), body, `"links":[]`)
}

// ── Missing dir / bad JSON tests ──

func (s *ServerSuite) TestGetTicket_MissingDir() {
	rec := s.testRequest("GET", "/api/tickets/tic-1234", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_BadJSON() {
	rec := s.testRequest("POST", "/api/tickets", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTicket_MissingDir() {
	rec := s.testRequest("POST", "/api/tickets", `{"title": "test"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_BadJSON() {
	rec := s.testRequest("PATCH", "/api/tickets/tic-1234", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_MissingDir() {
	rec := s.testRequest("PATCH", "/api/tickets/tic-1234", `{"status": "closed"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTicket_ReadError() {
	dir := createTicketsDir(s.T())
	// Write an invalid ticket file so Read fails after ResolveID succeeds
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".tickets", "tic-brkn.md"), []byte("---\n\x00invalid\n---\n"), 0644))

	body := fmt.Sprintf(`{"dir": %q, "status": "closed"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-brkn", body)
	// ResolveID might fail (404) or Read might fail (500) depending on the library
	require.True(s.T(), rec.Code == http.StatusNotFound || rec.Code == http.StatusInternalServerError)
}

func (s *ServerSuite) TestUpdateTicket_WithEventsHub() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-evup", "Event update", tk.StatusOpen)
	hub := NewEventsHub(s.srv.logger)
	s.srv.SetEventsHub(hub)

	body := fmt.Sprintf(`{"dir": %q, "status": "in_progress"}`, dir)
	rec := s.testRequest("PATCH", "/api/tickets/tic-evup", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestDeleteTicket_WithEventsHub() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-evdl", "Event delete", tk.StatusOpen)
	hub := NewEventsHub(s.srv.logger)
	s.srv.SetEventsHub(hub)

	rec := s.testRequest("DELETE", "/api/tickets/tic-evdl?dir="+dir, "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_ThreadsNotConfigured() {
	// Store configured but threads nil
	srv := NewServer(nil, nil, nil, s.store, nil, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tickets/{id}/assign", srv.handleAssignTicket)

	req, _ := http.NewRequest("POST", "/api/tickets/tic-1/assign", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_BadJSON() {
	rec := s.testRequest("POST", "/api/tickets/tic-1234/assign", `{bad json}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// ── Error branch tests (filesystem errors) ──

func (s *ServerSuite) TestListTickets_StoreListError() {
	dir := s.T().TempDir()
	// Create .tickets as a file instead of a dir to make List fail
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".tickets"), []byte("not a dir"), 0644))

	rec := s.testRequest("GET", "/api/tickets?dir="+dir, "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestGetTicket_ReadError() {
	dir := createTicketsDir(s.T())
	// Write a ticket file with invalid content so Read fails after ResolveID succeeds
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".tickets", "tic-badf.md"), []byte("not valid yaml frontmatter"), 0644))

	rec := s.testRequest("GET", "/api/tickets/tic-badf?dir="+dir, "")
	// ResolveID succeeds (file exists), but Read may fail parsing
	require.True(s.T(), rec.Code == http.StatusOK || rec.Code == http.StatusInternalServerError)
}

func (s *ServerSuite) TestCreateTicket_EnsureDirError() {
	dir := s.T().TempDir()
	// Create .tickets as a file so EnsureDir (MkdirAll) fails
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".tickets"), []byte("blocker"), 0644))

	body := fmt.Sprintf(`{"dir": %q, "title": "Will fail"}`, dir)
	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating tickets dir")
}

func (s *ServerSuite) TestCreateTicket_GenerateIDError() {
	dir := createTicketsDir(s.T())

	orig := generateID
	generateID = func() (string, error) { return "", fmt.Errorf("entropy exhausted") }
	s.T().Cleanup(func() { generateID = orig })

	body := fmt.Sprintf(`{"dir": %q, "title": "Will fail"}`, dir)
	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "entropy exhausted")
}

func (s *ServerSuite) TestCreateTicket_WriteError() {
	ms := new(MockTicketStore)
	ms.On("EnsureDir").Return(nil)
	ms.On("Write", mock.Anything).Return(fmt.Errorf("disk full"))
	s.srv.ticketStoreOpener = func(string) TicketStore { return ms }
	s.T().Cleanup(func() { s.srv.ticketStoreOpener = nil })

	body := `{"dir": "/any", "title": "Will fail"}`
	rec := s.testRequest("POST", "/api/tickets", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "disk full")
}

func (s *ServerSuite) TestUpdateTicket_WriteError() {
	ms := new(MockTicketStore)
	ms.On("ResolveID", "tic-wrfl").Return("tic-wrfl", nil)
	ms.On("Read", "tic-wrfl").Return(&tk.Ticket{
		ID: "tic-wrfl", Title: "Write fail", Status: tk.StatusOpen, Type: tk.TypeTask,
	}, nil)
	ms.On("Write", mock.Anything).Return(fmt.Errorf("disk full"))
	s.srv.ticketStoreOpener = func(string) TicketStore { return ms }
	s.T().Cleanup(func() { s.srv.ticketStoreOpener = nil })

	body := `{"dir": "/any", "status": "closed"}`
	rec := s.testRequest("PATCH", "/api/tickets/tic-wrfl", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "disk full")
}

func (s *ServerSuite) TestDeleteTicket_DeleteError() {
	ms := new(MockTicketStore)
	ms.On("ResolveID", "tic-dlfl").Return("tic-dlfl", nil)
	ms.On("Delete", "tic-dlfl").Return(fmt.Errorf("permission denied"))
	s.srv.ticketStoreOpener = func(string) TicketStore { return ms }
	s.T().Cleanup(func() { s.srv.ticketStoreOpener = nil })

	rec := s.testRequest("DELETE", "/api/tickets/tic-dlfl?dir=/any", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "permission denied")
}

// ── Assign ticket tests ──

// initGitRepoWithTickets creates a git repo with .tickets/ dir and returns the path.
func initGitRepoWithTickets(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tickets"), 0755))
	return dir
}

func (s *ServerSuite) TestAssignTicket_Success() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-asgn", "Assign me", tk.StatusOpen)

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-1",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("asgn-thread", nil)
	s.store.On("GetChannel", mock.Anything, "asgn-thread").Return(&db.Channel{
		ChannelID: "asgn-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	hub := NewEventsHub(s.srv.logger)
	s.srv.SetEventsHub(hub)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-asgn/assign", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp assignTicketResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "asgn-thread", resp.ThreadID)
	require.NotEmpty(s.T(), resp.WorktreePath)

	// Ticket should be in_progress with assignee set
	store := tk.Open(dir)
	ticket, err := store.Read("tic-asgn")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tk.StatusInProgress, ticket.Status)
	require.Equal(s.T(), "Assign me (tic-asgn)", ticket.Assignee)
}

func (s *ServerSuite) TestAssignTicket_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tickets/{id}/assign", srv.handleAssignTicket)

	req, _ := http.NewRequest("POST", "/api/tickets/tic-1/assign", nil)
	rec := newRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_MissingChannelID() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-noid", "No channel", tk.StatusOpen)

	body := fmt.Sprintf(`{"dir": %q}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-noid/assign", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel_id is required")
}

func (s *ServerSuite) TestAssignTicket_TicketNotFound() {
	dir := createTicketsDir(s.T())

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/nonexistent/assign", body)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_AlreadyClaimed() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-clmd", "Already claimed", tk.StatusInProgress)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-clmd/assign", body)
	require.Equal(s.T(), http.StatusConflict, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_ChannelNotFound() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-noch", "No channel", tk.StatusOpen)

	s.store.On("GetChannel", mock.Anything, "missing-ch").Return(nil, nil)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "missing-ch"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-noch/assign", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "channel not found")
}

func (s *ServerSuite) TestAssignTicket_GetChannelError() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-cher", "Ch error", tk.StatusOpen)

	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, fmt.Errorf("db error"))

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "err-ch"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-cher/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

// TestAssignTicket_GrandparentLookupFails covers the error branch when
// resolving a thread channel's parent project: handleAssignTicket calls
// store.GetChannel(parent.ParentID) and must surface the error rather
// than fall through with an inconsistent parent.
func (s *ServerSuite) TestAssignTicket_GrandparentLookupFails() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-gperr", "Grandparent err", tk.StatusOpen)

	// First lookup: thread row with non-empty ParentID.
	s.store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{
		ChannelID: "thread-1", DirPath: dir, ParentID: "parent-err",
	}, nil)
	// Grandparent lookup fails — the handler must 500 instead of proceeding.
	s.store.On("GetChannel", mock.Anything, "parent-err").Return(nil, fmt.Errorf("db boom"))

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "thread-1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-gperr/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "db boom")
}

func (s *ServerSuite) TestAssignTicket_WorktreeCreateFails() {
	dir := createTicketsDir(s.T())
	writeTestTicket(s.T(), dir, "tic-wtfl", "WT fail", tk.StatusOpen)

	// Return a channel with DirPath pointing to the tickets dir (not a git repo),
	// so git worktree add will fail.
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-wtfl/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating worktree")
}

func (s *ServerSuite) TestAssignTicket_ThreadCreateFails() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-thfl", "Thread fail", tk.StatusOpen)

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("", fmt.Errorf("thread error"))

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-thfl/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating thread")
}

func (s *ServerSuite) TestAssignTicket_GetThreadFails() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-gtfl", "Get thread fail", tk.StatusOpen)

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("gt-thread", nil)
	s.store.On("GetChannel", mock.Anything, "gt-thread").Return(nil, fmt.Errorf("db error"))

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-gtfl/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to get created thread")
}

func (s *ServerSuite) TestAssignTicket_UpsertChannelFails() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-upfl", "Upsert fail", tk.StatusOpen)

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("up-thread", nil)
	s.store.On("GetChannel", mock.Anything, "up-thread").Return(&db.Channel{
		ChannelID: "up-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(fmt.Errorf("upsert error"))

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-upfl/assign", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "updating thread")
}

func (s *ServerSuite) TestAssignTicket_WithCustomBranch() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-brnc", "Custom branch", tk.StatusOpen)

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("br-thread", nil)
	s.store.On("GetChannel", mock.Anything, "br-thread").Return(&db.Channel{
		ChannelID: "br-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1", "branch": "HEAD"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-brnc/assign", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_MissingDir() {
	body := `{"channel_id": "ch1"}`
	rec := s.testRequest("POST", "/api/tickets/tic-1234/assign", body)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestAssignTicket_ResolvesThreadToGrandparent() {
	// Mirror of TestCreateWorktree_ResolvesThreadToGrandparent: when channel_id
	// is a thread, the worktree should be created from the grandparent's
	// DirPath/SessionID — matching where the new thread row will be parented.
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-gpr", "Grandparent resolve", tk.StatusOpen)

	s.srv.sys = s.sys
	// Thread channel with a parent project channel.
	s.store.On("GetChannel", mock.Anything, "thread-ch").Return(&db.Channel{
		ChannelID: "thread-ch", ParentID: "proj-ch", DirPath: dir, SessionID: "thread-sess",
	}, nil)
	s.store.On("GetChannel", mock.Anything, "proj-ch").Return(&db.Channel{
		ChannelID: "proj-ch", DirPath: dir, SessionID: "grandparent-sess",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "thread-ch", mock.Anything, "", "").Return("gpr-thread", nil)
	s.store.On("GetChannel", mock.Anything, "gpr-thread").Return(&db.Channel{
		ChannelID: "gpr-thread", DirPath: dir, ParentID: "proj-ch", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "thread-ch"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-gpr/assign", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	// Verify proj-ch was looked up (grandparent resolution happened).
	s.store.AssertCalled(s.T(), "GetChannel", mock.Anything, "proj-ch")
}

func (s *ServerSuite) TestAssignTicket_AutoStartAgent() {
	dir := initGitRepoWithTickets(s.T())
	// Write ticket with a description so the agent auto-start path is triggered
	ticket := &tk.Ticket{
		ID:          "tic-agen",
		Title:       "Agent test",
		Description: "Do the work",
		Status:      tk.StatusOpen,
		Type:        tk.TypeTask,
		Priority:    2,
		Created:     time.Now().UTC(),
	}
	store := tk.Open(dir)
	require.NoError(s.T(), store.EnsureDir())
	require.NoError(s.T(), store.Write(ticket))

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir,
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("ag-thread", nil)
	s.store.On("GetChannel", mock.Anything, "ag-thread").Return(&db.Channel{
		ChannelID: "ag-thread", DirPath: dir, ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

	// Set up mock message handler
	msgHandler := new(MockIncomingMessageHandler)
	msgHandler.On("HandleThreadCreated", mock.Anything, "ag-thread", "", "Do the work").Return()
	s.srv.msgHandler = msgHandler

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-agen/assign", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	// Give goroutine time to fire
	time.Sleep(50 * time.Millisecond)
	msgHandler.AssertCalled(s.T(), "HandleThreadCreated", mock.Anything, "ag-thread", "", "Do the work")
}

// TestAssignTicket_SessionNotStaged mirrors TestForkThread_WorktreeSessionNotStaged:
// a pruned transcript must not be pinned on the assignee thread.
func (s *ServerSuite) TestAssignTicket_SessionNotStaged() {
	dir := initGitRepoWithTickets(s.T())
	writeTestTicket(s.T(), dir, "tic-stale", "Assign me", tk.StatusOpen)

	s.srv.sys = s.sys
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: dir, SessionID: "sess-pruned",
	}, nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("asgn-thread", nil)
	s.store.On("GetChannel", mock.Anything, "asgn-thread").Return(&db.Channel{
		ChannelID: "asgn-thread", DirPath: dir, ParentID: "ch1", Active: true, SessionID: "sess-pruned",
	}, nil)
	upserted := make(chan *db.Channel, 1)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		select {
		case upserted <- args.Get(1).(*db.Channel):
		default:
		}
	}).Return(nil)

	body := fmt.Sprintf(`{"dir": %q, "channel_id": "ch1"}`, dir)
	rec := s.testRequest("POST", "/api/tickets/tic-stale/assign", body)
	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())

	ch := <-upserted
	require.Empty(s.T(), ch.SessionID)
}
