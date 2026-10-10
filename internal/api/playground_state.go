package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
)

const (
	// playgroundStateFile holds a playground's state, beside its files so
	// an agent working on the playground can read it too.
	playgroundStateFile = "state.json"
	// maxPlaygroundStateBytes caps a state update and the stored state.
	maxPlaygroundStateBytes = 1 << 20
)

// errPlaygroundStateTooLarge is a state that would grow past
// maxPlaygroundStateBytes.
var errPlaygroundStateTooLarge = errors.New("playground state is larger than 1 MiB")

// playgroundStatePath resolves the state file of the playground r names
// (?name=&scope=&channel_id=), answering the request itself when it can't.
func (s *playgroundService) playgroundStatePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	if info, err := os.Stat(pgDir); err != nil || !info.IsDir() {
		http.Error(w, fmt.Sprintf("no playground %q", name), http.StatusNotFound)
		return "", false
	}
	path, err := s.playgroundFile(pgDir, playgroundStateFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return path, true
}

// readPlaygroundState reads the state at path; a playground without one has
// an empty state.
func readPlaygroundState(path string) (map[string]json.RawMessage, error) {
	state := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil || state == nil {
		// A hand-edited file that isn't an object starts over.
		return map[string]json.RawMessage{}, nil
	}
	return state, nil
}

// handlePlaygroundStateGet handles GET /api/playground/state — a
// playground's state, a JSON object its page keeps through
// window.loop.state.
func (s *playgroundService) handlePlaygroundStateGet(w http.ResponseWriter, r *http.Request) {
	path, ok := s.playgroundStatePath(w, r)
	if !ok {
		return
	}
	s.stateMu.Lock()
	state, err := readPlaygroundState(path)
	s.stateMu.Unlock()
	if err != nil {
		http.Error(w, "reading state: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, state, s.deps.logger)
}

// handlePlaygroundStatePatch handles PATCH /api/playground/state: the body,
// a JSON object, is merged into the state, a null value removing its key. It
// answers with the new state and tells the playground's panels, which pass
// it to their page.
func (s *playgroundService) handlePlaygroundStatePatch(w http.ResponseWriter, r *http.Request) {
	path, ok := s.playgroundStatePath(w, r)
	if !ok {
		return
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPlaygroundStateBytes+1)).Decode(&patch); err != nil || patch == nil {
		http.Error(w, "the body must be a JSON object of at most 1 MiB", http.StatusBadRequest)
		return
	}

	state, err := s.patchPlaygroundState(path, patch)
	if errors.Is(err, errPlaygroundStateTooLarge) {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "writing state: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.deps.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.deps.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": r.URL.Query().Get("name"), "scope": scope, "channel_id": channelID, "kind": "state"},
		})
	}
	writeHTTPJSON(w, http.StatusOK, state, s.deps.logger)
}

// patchPlaygroundState merges patch into the state at path and writes it.
func (s *playgroundService) patchPlaygroundState(path string, patch map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	state, err := readPlaygroundState(path)
	if err != nil {
		return nil, err
	}
	maps.Copy(state, patch)
	for k, v := range state {
		if string(v) == "null" {
			delete(state, k)
		}
	}
	data, _ := json.MarshalIndent(state, "", "  ") // decoded JSON values marshal
	if len(data) > maxPlaygroundStateBytes {
		return nil, errPlaygroundStateTooLarge
	}
	write := s.writeStateFile
	if write == nil {
		write = os.WriteFile
	}
	return state, write(path, data, 0o644)
}
