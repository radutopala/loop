package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agentgate"
)

// maxAuditLine caps an audit record's line; a longer one is skipped.
const maxAuditLine = 1 << 20

// Audit returns up to limit of the gate's records for a plugin's container
// in a channel, newest first. A channel without any has none.
func (m *Manager) Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error) {
	if !validPathPart(pluginID) || !validPathPart(channelID) {
		return nil, fmt.Errorf("invalid plugin %q or channel %q", pluginID, channelID)
	}
	dir := m.auditDir(pluginID, channelID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []agentgate.AuditEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the plugin gate audit dir: %w", err)
	}
	// agentgate-YYYY-MM-DD.jsonl sorts by date; read the newest first.
	var days []string
	for _, e := range entries {
		name := e.Name()
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "agentgate-"), ".jsonl")
		if e.Type().IsRegular() && name == "agentgate-"+stamp+".jsonl" {
			if _, err := time.Parse(time.DateOnly, stamp); err == nil {
				days = append(days, name)
			}
		}
	}
	slices.Sort(days)
	out := []agentgate.AuditEntry{}
	for _, name := range slices.Backward(days) {
		day, err := m.readAudit(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		for _, e := range slices.Backward(day) {
			if len(out) == limit {
				return out, nil
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// readAudit reads a day's records, oldest first, skipping lines that
// aren't one.
func (m *Manager) readAudit(path string) ([]agentgate.AuditEntry, error) {
	f, err := m.open(path)
	if err != nil {
		return nil, fmt.Errorf("opening the plugin gate audit: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, maxAuditLine)
	var out []agentgate.AuditEntry
	for {
		line, err := r.ReadSlice('\n')
		long := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		if !long {
			var e agentgate.AuditEntry
			if json.Unmarshal(line, &e) == nil {
				out = append(out, e)
			}
		}
		if err != nil {
			return out, nil
		}
	}
}

// validPathPart reports whether s names one path element.
func validPathPart(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}
