package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/unidiff"
)

// isLearnMemoryKind reports whether v, a decoded proposal payload, is one
// editLearnMemory writes to Claude Code's auto-memory.
func isLearnMemoryKind(v any) bool {
	_, ok := v.(*learn.Memory)
	return ok
}

// editLearnMemory adds a memory proposal to Claude Code's auto-memory for the
// channel's project (the root checkout's, for a worktree thread, whose
// agents read the root's too): the memory's own file, and its pointer in
// MEMORY.md. It returns the memory file's path and a diff of both files.
// With write false it only works out the edit, for the preview. The index
// stays locked from the duplicate check to the write, so proposals applied
// at once (Apply all) don't drop each other's pointers.
func (s *Server) editLearnMemory(ctx context.Context, ch *db.Channel, m *learn.Memory, write bool) (string, string, error) {
	dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID)
	if err != nil {
		return "", "", err
	}
	memDir, err := claudeMemoryDir(s.sys, dir)
	if err != nil {
		return "", "", err
	}
	path, indexPath := filepath.Join(memDir, m.File()), filepath.Join(memDir, learn.MemoryIndex)
	defer s.configLocks.lock(indexPath)()
	if _, err := s.sys.Stat(path); err == nil {
		return path, "", fmt.Errorf("a memory named %q already exists", m.Name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return path, "", fmt.Errorf("checking the memory file: %w", err)
	}
	index, err := s.sys.ReadFile(indexPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return path, "", fmt.Errorf("reading %s: %w", learn.MemoryIndex, err)
	}
	indexFrom := indexPath
	if err != nil {
		indexFrom = "/dev/null"
	}
	content, newIndex := m.Content(), string(index)
	if !m.Indexed(newIndex) {
		if newIndex != "" && newIndex[len(newIndex)-1] != '\n' {
			newIndex += "\n"
		}
		newIndex += m.IndexLine() + "\n"
	}
	if write {
		if err := s.sys.MkdirAll(memDir, 0o700); err != nil {
			return path, "", fmt.Errorf("creating the memory dir: %w", err)
		}
		if err := s.sys.WriteFile(path, []byte(content), 0o644); err != nil {
			return path, "", fmt.Errorf("writing the memory file: %w", err)
		}
		if newIndex != string(index) {
			if err := s.sys.WriteFile(indexPath, []byte(newIndex), 0o644); err != nil {
				return path, "", fmt.Errorf("writing %s: %w", learn.MemoryIndex, err)
			}
		}
	}
	diff := unidiff.Diff("/dev/null", path, "", content) + unidiff.Diff(indexFrom, indexPath, string(index), newIndex)
	return path, diff, nil
}
