//go:build component

package component

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/cucumber/godog"
)

const (
	backupBlockStart = "// bdd backup start"
	backupBlockEnd   = "// bdd backup end"
)

var backupFileName = regexp.MustCompile(`^loop-(\d{8}T\d{6}Z)\.zip(\.age)?$`)

// setGlobalBackupConfig saves the global config through the API with its
// backup settings set to settings, writing to dir of the global .loop dir.
// It replaces the block an earlier call added.
func (tc *TestContext) setGlobalBackupConfig(dir string, settings *godog.DocString) error {
	path, raw, err := tc.globalConfigFile()
	if err != nil {
		return err
	}
	var backup map[string]any
	if err := json.Unmarshal([]byte(settings.Content), &backup); err != nil {
		return fmt.Errorf("backup settings: %w", err)
	}
	backup["path"] = filepath.Join(filepath.Dir(path), dir)
	block, err := json.Marshal(backup)
	if err != nil {
		return err
	}
	if start, end := strings.Index(raw, backupBlockStart), strings.Index(raw, backupBlockEnd); start >= 0 && end > start {
		raw = raw[:start] + raw[end+len(backupBlockEnd)+1:]
	}
	open := strings.Index(raw, "{")
	if open < 0 {
		return fmt.Errorf("global config isn't an object: %q", raw)
	}
	content := raw[:open+1] + "\n" + backupBlockStart + "\n\"backup\": " + string(block) + ",\n" + backupBlockEnd + "\n" + raw[open+1:]
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	if err := tc.doRequest(http.MethodPut, "/api/config", string(body)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusNoContent {
		return fmt.Errorf("saving global config: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	return nil
}

// globalBackups lists the backup files in dir of the global .loop dir,
// oldest first, each as its path and modification time, so a backup
// written again under the same name, in the same second, is new.
func (tc *TestContext) globalBackups(dir string) ([]string, error) {
	path, err := tc.globalLoopFile(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !backupFileName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		names = append(names, filepath.Join(path, e.Name())+"@"+info.ModTime().Format(time.RFC3339Nano))
	}
	slices.Sort(names)
	return names, nil
}

// waitForBackups waits until dir holds count backups, the newest of them
// written since the scenario remembered them, each holding a SQLite
// database.
func (tc *TestContext) waitForBackups(dir string, count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		names, err := tc.globalBackups(dir)
		if err != nil {
			return err
		}
		if len(names) == count && (len(names) == 0 || !slices.Contains(tc.seenBackups, names[len(names)-1])) {
			for _, name := range names {
				if _, err := tc.openBackup(backupPath(name)); err != nil {
					return err
				}
			}
			tc.seenBackups = names
			return tc.waitForBackupDone(deadline)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: want %d new backups, have %v", dir, count, names)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitForBackupDone waits until the daemon no longer reports a backup
// running; it prunes after the new backup appears.
func (tc *TestContext) waitForBackupDone(deadline time.Time) error {
	for {
		if err := tc.doRequest(http.MethodGet, "/api/backup", ""); err != nil {
			return err
		}
		if tc.LastStatus != http.StatusOK {
			return fmt.Errorf("backup status: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
		}
		if tc.LastJSON["running"] == false {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("backup still running: %s", string(tc.LastBody))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (tc *TestContext) waitForScheduledBackups(count int, dir string) error {
	// The daemon checks every minute whether a backup is due.
	return tc.waitForBackups(dir, count, 75*time.Second)
}

func (tc *TestContext) waitForStartedBackups(count int, dir string) error {
	return tc.waitForBackups(dir, count, 30*time.Second)
}

// backupPath strips the modification time globalBackups adds.
func backupPath(name string) string {
	path, _, _ := strings.Cut(name, "@")
	return path
}

// openBackup reads a backup, decrypting it with the key the scenario
// created when it's encrypted, and checks that it holds a SQLite database.
func (tc *TestContext) openBackup(name string) (*zip.Reader, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(name, ".age") {
		id, err := age.ParseX25519Identity(tc.backupIdentity)
		if err != nil {
			return nil, fmt.Errorf("%s: the scenario's key: %w", name, err)
		}
		r, err := age.Decrypt(bytes.NewReader(data), id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if data, err = io.ReadAll(r); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	db, err := zr.Open("loop.db")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer db.Close()
	header := make([]byte, 16)
	if _, err := io.ReadFull(db, header); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if string(header) != "SQLite format 3\x00" {
		return nil, fmt.Errorf("%s isn't a SQLite database: %q", name, header)
	}
	return zr, nil
}

// newestBackup returns the path of the newest backup in dir of the global
// .loop dir.
func (tc *TestContext) newestBackup(dir string) (string, error) {
	names, err := tc.globalBackups(dir)
	if err != nil || len(names) == 0 {
		return "", fmt.Errorf("no backups in %s: %v", dir, err)
	}
	return backupPath(names[len(names)-1]), nil
}

// assertNewestBackupHolds checks that the newest backup in dir is
// encrypted, or not, and holds entry.
func (tc *TestContext) assertNewestBackupHolds(dir, encrypted, entry string) error {
	path, err := tc.newestBackup(dir)
	if err != nil {
		return err
	}
	if want := encrypted == "an encrypted"; strings.HasSuffix(path, ".age") != want {
		return fmt.Errorf("%s: want encrypted %v", path, want)
	}
	zr, err := tc.openBackup(path)
	if err != nil {
		return err
	}
	f, err := zr.Open(entry)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return f.Close()
}

// waitPastNewestBackup waits until the second the newest backup in dir is
// named after has passed: a backup is never written over another, so a
// second backup in the same second fails.
func (tc *TestContext) waitPastNewestBackup(dir string) error {
	path, err := tc.newestBackup(dir)
	if err != nil {
		return err
	}
	at, err := time.Parse("20060102T150405Z", backupFileName.FindStringSubmatch(filepath.Base(path))[1])
	if err != nil {
		return err
	}
	time.Sleep(time.Until(at.Add(time.Second)))
	return nil
}

// createBackupKey creates the key pair backups are encrypted with, and
// remembers the private key to decrypt them.
func (tc *TestContext) createBackupKey() error {
	if err := tc.doRequest(http.MethodPost, "/api/backup/key", "{}"); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("creating the backup key: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	recipient, _ := tc.LastJSON["recipient"].(string)
	identity, _ := tc.LastJSON["identity"].(string)
	if !strings.HasPrefix(recipient, "age1") || !strings.HasPrefix(identity, "AGE-SECRET-KEY-1") {
		return fmt.Errorf("backup key: want age1... and AGE-SECRET-KEY-1..., got %q", recipient)
	}
	tc.backupIdentity = identity
	return nil
}

// assertBackupStatusLast checks that GET /api/backup's newest backup is the
// newest file in dir.
func (tc *TestContext) assertBackupStatusLast(dir string) error {
	// Read before globalBackups, whose request replaces the response.
	last, _ := tc.LastJSON["last"].(map[string]any)
	names, err := tc.globalBackups(dir)
	if err != nil || len(names) == 0 {
		return fmt.Errorf("no backups in %s: %v", dir, err)
	}
	if want, got := backupPath(names[len(names)-1]), fmt.Sprint(last["path"]); got != want {
		return fmt.Errorf("last backup: want %s, got %s", want, got)
	}
	return nil
}

// removeGlobalLoopDir removes dir of the global .loop dir, left by an
// earlier run.
func (tc *TestContext) removeGlobalLoopDir(dir string) error {
	path, err := tc.globalLoopFile(dir)
	if err != nil {
		return err
	}
	tc.seenBackups = nil
	return os.RemoveAll(path)
}
