// backup.go serves the backup's status, starts a backup and creates the
// key pair backups are encrypted with.
package api

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/radutopala/loop/internal/config/hjsonedit"

	"github.com/radutopala/loop/internal/backup"
)

// BackupRunner writes database backups and reports how they went.
type BackupRunner interface {
	Status() backup.Status
	Start() error
}

// SetBackup configures the database backup behind /api/backup.
func (s *Server) SetBackup(b BackupRunner) {
	s.backup = b
}

// handleGetBackup returns the backup schedule, the newest backup and the
// last failure.
func (s *Server) handleGetBackup(w http.ResponseWriter, _ *http.Request) {
	if s.backup == nil {
		http.Error(w, "backup not configured", http.StatusNotImplemented)
		return
	}
	writeHTTPJSON(w, http.StatusOK, s.backup.Status(), s.logger)
}

// handleStartBackup starts a backup now, whether or not backups are
// scheduled; GET /api/backup tells when it's done.
func (s *Server) handleStartBackup(w http.ResponseWriter, _ *http.Request) {
	if s.backup == nil {
		http.Error(w, "backup not configured", http.StatusNotImplemented)
		return
	}
	switch err := s.backup.Start(); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, backup.ErrRunning):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// backupKeyResponse is a new key pair for encrypting backups.
type backupKeyResponse struct {
	// Recipient is the public key (age1...), now in the global config's
	// backup.recipient.
	Recipient string `json:"recipient"`
	// Identity is the private key (AGE-SECRET-KEY-1...). Loop doesn't keep
	// it: this response is the only place it's shown.
	Identity string `json:"identity"`
}

// handleCreateBackupKey generates a key pair for encrypting backups, sets
// the public key as the global config's backup.recipient and returns both
// keys. Loop keeps no copy of the private key.
func (s *Server) handleCreateBackupKey(w http.ResponseWriter, _ *http.Request) {
	if s.loopDir == "" {
		http.Error(w, "loop directory not configured", http.StatusInternalServerError)
		return
	}
	id, err := s.newBackupKey()
	if err != nil {
		http.Error(w, "generating the key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	recipient := id.Recipient().String()
	path := filepath.Join(s.loopDir, configFileName)
	unlock := s.lockConfig(path, "settings")
	_, _, err = hjsonedit.SetData(s.sys, path, []string{"backup", "recipient"}, recipient)
	unlock()
	if err != nil {
		http.Error(w, "saving the public key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, backupKeyResponse{Recipient: recipient, Identity: id.String()}, s.logger)
}
